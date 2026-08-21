package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/incident"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/metrics"
	"github.com/ivantit66/onebase/internal/processcontrol"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/version"
	"github.com/ivantit66/onebase/internal/webhook"
	"github.com/ivantit66/onebase/internal/websec"
)

type Server struct {
	srv         *http.Server
	handler     http.Handler
	frontend    Frontend
	hooks       *webhook.Dispatcher
	processDone <-chan struct{}
	h2c         bool // cleartext HTTP/2 к апстриму включён (ONEBASE_H2C), план 111 P2-1
}

// New строит HTTP-сервер базы. host «» = 127.0.0.1 (см. addr.go): наружу
// сервер выставляется только явным --host 0.0.0.0.
func New(reg *runtime.Registry, store *storage.DB, interp *interpreter.Interpreter, authRepo *auth.Repo, host string, port int, cfg Config, frontend Frontend, sched *scheduler.Scheduler) *Server {
	// Debug API защищён внутренним токеном. Без него (плоский `onebase run`,
	// опубликованная база) debug-маршруты не монтируются вовсе.
	debugToken := cfg.DebugToken
	if debugToken != "" && cfg.Metrics == nil {
		cfg.Metrics = metrics.New()
	}
	controlToken := os.Getenv("ONEBASE_CONTROL_TOKEN")
	baseID := os.Getenv("ONEBASE_BASE_ID")
	processDone := make(chan struct{})
	var processStopOnce sync.Once
	processInstance, processInstanceErr := processcontrol.NewNonce()
	// Единый лимитер попыток входа: форма /login и basic-auth HTTP-сервисов
	// троттлятся вместе, чтобы брутфорс нельзя было размазать по двум каналам.
	loginLimit := cfg.LoginLimit
	if loginLimit == nil {
		loginLimit = auth.NewLoginLimiter(5, time.Minute)
	}
	if cfg.Metrics != nil {
		registerRuntimeMetrics(cfg.Metrics, authRepo, frontend, sched, cfg.Webhooks)
	}
	h := &handler{
		reg: reg, store: store, interp: interp, entitySvc: frontend.EntitySvc(), hooks: cfg.Webhooks,
		maxFileSizeBytes:       int64(cfg.MaxFileSizeMB) * 1024 * 1024,
		allowedAttachmentTypes: cfg.AllowedTypes,
	}
	r := chi.NewRouter()
	r.Use(requestLogger()) // как middleware.Logger, но режет токены/коды из URI (план 53)
	// Вместо chi middleware.Recoverer: тот же перехват, но паника получает код
	// инцидента, который виден пользователю и подставляется в «Сообщить об
	// ошибке» вместе со стеком (план 116).
	r.Use(incident.Recoverer(frontend.Incidents(), func(r *http.Request) string {
		if u := auth.UserFromContext(r.Context()); u != nil {
			return u.Login
		}
		return ""
	}))
	r.Use(websec.SecurityHeaders) // nosniff, Referrer-Policy, CSP frame-ancestors (план 53)
	r.Use(csrfExceptServices)     // CSRF для всего, кроме /hs/* (у сервисов своя CORS-модель, см. serviceDispatch)

	// Сбор HTTP-метрик включаем тем же знаком, что и debug-поверхность: если
	// задан ONEBASE_DEBUG_TOKEN. Middleware ставим до маршрутов, чтобы он
	// оборачивал весь роутер; сам /metrics монтируется ниже под токен-гейтом.
	if debugToken != "" {
		r.Use(cfg.Metrics.Middleware)
	}

	// Public auth routes (no authentication required)
	publicURL := strings.TrimSpace(os.Getenv("ONEBASE_PUBLIC_URL"))
	authH := &auth.Handlers{
		Repo:    authRepo,
		Auditor: store,
		Codes:   auth.NewOneTimeCodes(30 * time.Second),
		// Secure-куки: явный ONEBASE_SECURE_COOKIES ИЛИ https в публичном адресе
		// (SEC-05). За HTTPS-терминатором r.TLS у запроса пустой, и без этого
		// вывода куки уходили бы без флага Secure, хотя соединение до
		// пользователя зашифровано.
		SecureCookies: envBool("ONEBASE_SECURE_COOKIES") || strings.HasPrefix(strings.ToLower(publicURL), "https://"),
		// 5 неудач с одного IP по одному логину → блок на минуту (план 53).
		// Общий с basic-auth HTTP-сервисов (см. uiCfg.LoginLimit).
		LoginLimit: loginLimit,
		// Имя базы попадает в otpauth-ссылку — в аутентификаторе рядом с кодом
		// видно, к какой базе он относится (план 84).
		AppName: cfg.AppName,
		// Внешний адрес для redirect_uri провайдера SSO: за обратным прокси
		// Host запроса не совпадает с публичным адресом.
		BaseURL: publicURL,
	}
	r.Get("/login", authH.LoginPage)
	r.Post("/login", authH.LoginSubmit)
	r.Post("/logout", authH.Logout)
	r.Get("/auth/status", authH.Status)
	r.Post("/auth/login", authH.LoginJSON)
	r.Get("/auth/bootstrap", authH.Bootstrap)
	// Второй фактор (план 84): шаг между паролем и сессией.
	r.Get("/login/2fa", authH.TwoFactorPage)
	r.Post("/login/2fa", authH.TwoFactorSubmit)
	r.Get("/login/2fa/qr", authH.TwoFactorQR)
	r.Post("/auth/2fa", authH.TwoFactorJSON)
	// Единый вход (план 84). Маршруты монтируются всегда: без настроенных
	// провайдеров они отвечают 404, а условное монтирование потребовало бы
	// перезапуска базы после добавления провайдера в админке.
	r.Get("/auth/oidc/{provider}/start", authH.OIDCStart)
	r.Get("/auth/oidc/{provider}/callback", authH.OIDCCallback)
	// Одноразовый код для bootstrap (план 53): хендлер сам проверяет session
	// cookie (401 JSON, без HTML-редиректа auth-мидлвары).
	r.Post("/auth/one-time-code", authH.IssueOneTimeCode)
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/healthz", healthzHandler(store))

	// Управление жизненным циклом базы из лаунчера. Persistent control secret
	// никогда не передаётся по HTTP: identity доказывается challenge-response
	// HMAC, а stop подписан для конкретного instance процесса. Debug bearer
	// намеренно отдельный и живёт только в запустившем launcher-процессе.
	if controlToken != "" && baseID != "" && processInstanceErr == nil {
		r.Route("/debug/process", func(r chi.Router) {
			r.Get("/identity", func(w http.ResponseWriter, req *http.Request) {
				challenge := req.URL.Query().Get(processcontrol.ChallengeQuery)
				if !processcontrol.ValidNonce(challenge) {
					http.Error(w, "invalid challenge", http.StatusBadRequest)
					return
				}
				identity := processcontrol.Identity{
					BaseID:   baseID,
					PID:      os.Getpid(),
					Instance: processInstance,
				}
				identity.Proof = processcontrol.IdentityProof(controlToken, identity.BaseID,
					identity.PID, identity.Instance, challenge)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(identity)
			})
			r.Post("/stop", func(w http.ResponseWriter, req *http.Request) {
				nonce := req.Header.Get(processcontrol.HeaderNonce)
				instance := req.Header.Get(processcontrol.HeaderInstance)
				want := processcontrol.StopProof(controlToken, baseID, instance, nonce)
				if req.Header.Get(processcontrol.HeaderBaseID) != baseID ||
					instance != processInstance || !processcontrol.ValidNonce(nonce) ||
					!processcontrol.Verify(req.Header.Get(processcontrol.HeaderProof), want) {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
				processStopOnce.Do(func() { close(processDone) })
			})
		})
	}

	// PWA-ассеты (manifest, service worker, offline-страница, иконки) — публичны.
	// Браузер фечит manifest/иконки без credentials, а install-промпт работает
	// вне сессии: под auth-мидлварой они отдавали бы 401 и PWA не устанавливался
	// бы на инстансе с пользователями. Ассеты не содержат данных (план 45).
	frontend.MountPWA(r)

	// HTTP-сервисы конфигурации (план 61) — /hs/<корень>/…. Монтируются ВНЕ
	// session-middleware: каждый сервис сам объявляет аутентификацию
	// (none/basic/session/token/hmac), поэтому публичные приёмники вебхуков
	// работают без cookie, а защищённые проверяют свой механизм внутри.
	frontend.MountServices(r)

	// Онлайн-обмен между базами (план 86) — /exchange/<план>/push|pull. Тоже вне
	// session-middleware: базы аутентифицируются общим Bearer-токеном плана.
	frontend.MountExchange(r)

	// Встроенная статика — вендор-ассеты (Monaco/ECharts/SlickGrid/Quill) и
	// app-JS. Несекретны и одинаковы для всех, поэтому монтируются ВНЕ
	// auth-мидлвары (план 111, P1-2): иначе каждый чанк Monaco и каждая
	// ревалидация app-JS (no-cache → 304) проходили через сессионную
	// авторизацию. Вендор уже отдаётся с immutable-кэшем; app-JS сохраняет
	// ETag-ревалидацию, но больше не платит за auth на каждый 304.
	frontend.MountStatic(r)

	// REST API v2 accepts either an integration Bearer token or the existing
	// browser session cookie. Keep it outside the UI/session-only group so
	// headless clients do not need a cookie.
	r.Group(func(r chi.Router) {
		r.Use(authRepo.APITokenOrSessionMiddleware)
		h.mountV2(r)
	})

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(authRepo.Middleware)

		// REST API — catalogs
		r.Get("/catalogs/{entity}", h.listObjects(metadata.KindCatalog))
		r.Post("/catalogs/{entity}", h.createObject(metadata.KindCatalog))
		r.Get("/catalogs/{entity}/{id}", h.getObject(metadata.KindCatalog))
		r.Put("/catalogs/{entity}/{id}", h.updateObject(metadata.KindCatalog))
		r.Delete("/catalogs/{entity}/{id}", h.deleteObject(metadata.KindCatalog))
		// REST API — documents
		r.Get("/documents/{entity}", h.listObjects(metadata.KindDocument))
		r.Post("/documents/{entity}", h.createObject(metadata.KindDocument))
		r.Get("/documents/{entity}/{id}", h.getObject(metadata.KindDocument))
		r.Put("/documents/{entity}/{id}", h.updateObject(metadata.KindDocument))
		r.Delete("/documents/{entity}/{id}", h.deleteObject(metadata.KindDocument))
		// Posting/un-posting документа (аналог UI-кнопки «Провести»).
		r.Post("/documents/{entity}/{id}/post", h.postDocument())

		// Web UI
		frontend.Mount(r)

		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/ui", http.StatusFound)
		})
	})

	// Debug API — токен-гейт (см. MountDebug). Монтируем только если токен
	// задан: без него опубликованная база не имеет debug-поверхности.
	// Туда же вешаем pprof — профилирование под тем же токеном (см. mountPprof).
	if debugToken != "" {
		frontend.MountDebug(r)
		mountPprof(r, debugToken)
		mountMetrics(r, debugToken, cfg.Metrics, store)
	}

	// h2c включается на самом сервере (Protocols), а не оборачиванием handler:
	// s.handler остаётся голым роутером для in-process монтирования
	// (Handler()/httptest). Опционально и по умолчанию выключено (ONEBASE_H2C).
	enableH2C := h2cEnabled()
	httpSrv := &http.Server{
		Addr:    listenAddr(host, port),
		Handler: r,
		// Slowloris-защита: обрываем клиента, который медленно шлёт заголовки,
		// и закрываем простаивающие keep-alive соединения. ReadTimeout/
		// WriteTimeout НАМЕРЕННО не выставлены — они оборвали бы загрузку
		// крупных .obz при восстановлении, SSE-стрим отладчика и скачивание
		// бэкапов. Тело запроса ограничивается отдельными MaxBytesReader.
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	configureH2C(httpSrv, enableH2C)
	return &Server{handler: r, frontend: frontend, hooks: cfg.Webhooks,
		processDone: processDone, h2c: enableH2C, srv: httpSrv}
}

func envBool(name string) bool {
	v, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(name)))
	return err == nil && v
}

// InvalidateWidgetCache makes metadata hot reload immediately visible on the
// dashboard instead of waiting for the widget TTL.
func (s *Server) InvalidateWidgetCache() {
	if s != nil && s.frontend != nil {
		s.frontend.InvalidateWidgetCache()
	}
}

// InvalidateServiceCache сбрасывает кэш ответов HTTP-сервисов (план 126).
func (s *Server) InvalidateServiceCache() {
	if s != nil && s.frontend != nil {
		s.frontend.InvalidateServiceCache()
	}
}

// ResyncWSIntakes приводит WS-соединения приёмки к текущему реестру (план
// 120A): вызывается при старте сервера и после горячей перезагрузки проекта.
func (s *Server) ResyncWSIntakes() {
	if s != nil && s.frontend != nil {
		s.frontend.ResyncWSIntakes()
	}
}

// PublishDevReload sends the trusted system envelope used by browser sync.
// DSL notifications cannot construct this envelope even if they reuse the
// visible event name.
func (s *Server) PublishDevReload() {
	if s != nil && s.frontend != nil {
		s.frontend.PublishDevReload()
	}
}

// healthzHandler — readiness-проба: 200, только если БД отвечает, иначе 503.
// Публична и без токена (в отличие от /metrics): её дёргают reverse-proxy,
// systemd WatchdogSec и команда `onebase update` при проверке нового бинаря.
// В отличие от liveness-/health (всегда 200), проверяет реальную доступность БД.
func healthzHandler(store *storage.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-OneBase-Version", version.String())
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		if err := store.Ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("db unavailable\n"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	}
}

// csrfExceptServices применяет websec.CSRFProtect ко всему роутеру, КРОМЕ
// поверхности /hs/* HTTP-сервисов. У сервисов своя модель доступа: аутентификация
// по заголовкам (token/hmac/basic) или none, плюс объявленная CORS-политика;
// глобальный CSRF (Origin≠Host → 403) ломал бы заявленный сервисом cross-origin
// доступ для POST/PUT/DELETE. CSRF-эквивалент для сервисов реализован внутри
// serviceDispatch (мутирующий запрос с чужим Origin пропускается, только если
// источник разрешён CORS сервиса).
func csrfExceptServices(next http.Handler) http.Handler {
	protected := websec.CSRFProtect(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hs" || strings.HasPrefix(r.URL.Path, "/hs/") {
			next.ServeHTTP(w, r)
			return
		}
		if (r.URL.Path == "/api/v2" || strings.HasPrefix(r.URL.Path, "/api/v2/")) && hasBearerAuthorization(r) {
			next.ServeHTTP(w, r)
			return
		}
		protected.ServeHTTP(w, r)
	})
}

func hasBearerAuthorization(r *http.Request) bool {
	parts := strings.Fields(r.Header.Get("Authorization"))
	return len(parts) > 0 && strings.EqualFold(parts[0], "Bearer")
}

func (s *Server) Handler() http.Handler { return s.handler }

// Done закрывается, когда аутентифицированный launcher попросил процесс базы
// корректно завершиться. Обычные запуски без launcher-токена этот канал не
// закрывают и по-прежнему завершаются только сигналом ОС.
func (s *Server) Done() <-chan struct{} { return s.processDone }

// H2CEnabled сообщает, обслуживает ли сетевой listener cleartext HTTP/2 (h2c).
// Используется CLI для строки о режиме в баннере старта (план 111, P2-1).
func (s *Server) H2CEnabled() bool { return s != nil && s.h2c }

func (s *Server) ListenAndServe() error {
	return s.srv.ListenAndServe()
}

// Listen reserves the configured address synchronously. CLI callers use it
// before announcing readiness or opening a browser, so a foreign /health on
// an occupied port can never be mistaken for this server.
func (s *Server) Listen() (net.Listener, error) {
	return net.Listen("tcp", s.srv.Addr)
}

// Serve serves an already-reserved listener.
func (s *Server) Serve(listener net.Listener) error {
	return s.srv.Serve(listener)
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.frontend != nil {
		s.frontend.BeginShutdown()
	}
	httpErr := s.srv.Shutdown(ctx)
	var uiErr error
	if s.frontend != nil {
		uiErr = s.frontend.Shutdown(ctx)
	}
	var hookErr error
	if s.hooks != nil {
		hookErr = s.hooks.Close(ctx)
	}
	return errors.Join(httpErr, uiErr, hookErr)
}
