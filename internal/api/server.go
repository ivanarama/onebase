package api

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/metrics"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
	"github.com/ivantit66/onebase/internal/websec"
)

type Server struct {
	srv     *http.Server
	handler http.Handler
}

// New строит HTTP-сервер базы. host «» = 127.0.0.1 (см. addr.go): наружу
// сервер выставляется только явным --host 0.0.0.0.
func New(reg *runtime.Registry, store *storage.DB, interp *interpreter.Interpreter, authRepo *auth.Repo, host string, port int, uiCfg ui.Config, sched *scheduler.Scheduler) *Server {
	// Debug API защищён внутренним токеном. Без него (плоский `onebase run`,
	// опубликованная база) debug-маршруты не монтируются вовсе.
	debugToken := os.Getenv("ONEBASE_DEBUG_TOKEN")
	uiCfg.DebugToken = debugToken
	// Единый лимитер попыток входа: форма /login и basic-auth HTTP-сервисов
	// троттлятся вместе, чтобы брутфорс нельзя было размазать по двум каналам.
	loginLimit := auth.NewLoginLimiter(5, time.Minute)
	uiCfg.LoginLimit = loginLimit
	var metricsReg *metrics.Registry
	if debugToken != "" {
		metricsReg = metrics.New()
		uiCfg.Metrics = metricsReg
	}
	uiSrv := ui.New(reg, store, interp, authRepo, uiCfg, sched)
	if metricsReg != nil {
		registerRuntimeMetrics(metricsReg, authRepo, uiSrv, sched, uiCfg.Webhooks)
	}
	h := &handler{reg: reg, store: store, interp: interp, entitySvc: uiSrv.EntitySvc()}
	r := chi.NewRouter()
	r.Use(requestLogger()) // как middleware.Logger, но режет токены/коды из URI (план 53)
	r.Use(middleware.Recoverer)
	r.Use(websec.SecurityHeaders) // nosniff, Referrer-Policy, CSP frame-ancestors (план 53)
	r.Use(csrfExceptServices)     // CSRF для всего, кроме /hs/* (у сервисов своя CORS-модель, см. serviceDispatch)

	// Сбор HTTP-метрик включаем тем же знаком, что и debug-поверхность: если
	// задан ONEBASE_DEBUG_TOKEN. Middleware ставим до маршрутов, чтобы он
	// оборачивал весь роутер; сам /metrics монтируется ниже под токен-гейтом.
	if debugToken != "" {
		r.Use(metricsReg.Middleware)
	}

	// Public auth routes (no authentication required)
	authH := &auth.Handlers{
		Repo:    authRepo,
		Auditor: store,
		Codes:   auth.NewOneTimeCodes(30 * time.Second),
		// 5 неудач с одного IP по одному логину → блок на минуту (план 53).
		// Общий с basic-auth HTTP-сервисов (см. uiCfg.LoginLimit).
		LoginLimit: loginLimit,
	}
	r.Get("/login", authH.LoginPage)
	r.Post("/login", authH.LoginSubmit)
	r.Post("/logout", authH.Logout)
	r.Get("/auth/status", authH.Status)
	r.Post("/auth/login", authH.LoginJSON)
	r.Get("/auth/bootstrap", authH.Bootstrap)
	// Одноразовый код для bootstrap (план 53): хендлер сам проверяет session
	// cookie (401 JSON, без HTML-редиректа auth-мидлвары).
	r.Post("/auth/one-time-code", authH.IssueOneTimeCode)
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	// PWA-ассеты (manifest, service worker, offline-страница, иконки) — публичны.
	// Браузер фечит manifest/иконки без credentials, а install-промпт работает
	// вне сессии: под auth-мидлварой они отдавали бы 401 и PWA не устанавливался
	// бы на инстансе с пользователями. Ассеты не содержат данных (план 45).
	uiSrv.MountPWA(r)

	// HTTP-сервисы конфигурации (план 61) — /hs/<корень>/…. Монтируются ВНЕ
	// session-middleware: каждый сервис сам объявляет аутентификацию
	// (none/basic/session/token/hmac), поэтому публичные приёмники вебхуков
	// работают без cookie, а защищённые проверяют свой механизм внутри.
	uiSrv.MountServices(r)

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

		// REST API v2 — envelope/meta, page/filter[] and OpenAPI contract.
		h.mountV2(r)

		// Web UI
		uiSrv.Mount(r)

		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/ui", http.StatusFound)
		})
	})

	// Debug API — токен-гейт (см. MountDebug). Монтируем только если токен
	// задан: без него опубликованная база не имеет debug-поверхности.
	// Туда же вешаем pprof — профилирование под тем же токеном (см. mountPprof).
	if debugToken != "" {
		uiSrv.MountDebug(r)
		mountPprof(r, debugToken)
		mountMetrics(r, debugToken, metricsReg, store)
	}

	return &Server{handler: r, srv: &http.Server{
		Addr:    listenAddr(host, port),
		Handler: r,
		// Slowloris-защита: обрываем клиента, который медленно шлёт заголовки,
		// и закрываем простаивающие keep-alive соединения. ReadTimeout/
		// WriteTimeout НАМЕРЕННО не выставлены — они оборвали бы загрузку
		// крупных .obz при восстановлении, SSE-стрим отладчика и скачивание
		// бэкапов. Тело запроса ограничивается отдельными MaxBytesReader.
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}}
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
		protected.ServeHTTP(w, r)
	})
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) ListenAndServe() error {
	return s.srv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}
