package ui

// HTTP-сервисы (план 61) — серверная сторона по аналогии с «HTTPСервис» 1С.
// Конфигурация публикует собственные REST-эндпоинты под /hs/<корень>/…, а их
// обработчики пишутся на DSL (src/<имя>.service.os). Здесь — маршрутизация,
// аутентификация по сервису и запуск обработчика с полным набором DSL-
// переменных (тот же, что у обработок: Запрос/Документы/Справочники/…).

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/httpservice"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/secrets"
	"github.com/ivantit66/onebase/internal/storage"
)

// resolveAuthSecret разыменовывает секрет входящего контура — HTTP-сервиса или
// шлюза приёма — в момент проверки вызова: в конфигурации лежит ссылка
// env:/file:/enc:, а не значение (план 83).
//
// Любая неудача — 401: контур, чей секрет не удалось получить, обязан не пускать
// никого. Причина уходит в журнал администратору, а не вызывающему: наружу
// нельзя раскрывать даже то, задан ли секрет вообще.
func resolveAuthSecret(ref, kind, name string, w http.ResponseWriter) (string, bool) {
	v, err := secrets.Default().Resolve(ref)
	if err != nil {
		oblog.Component("secrets").Warn("секрет входящего контура не разыменован",
			"вид", kind, "имя", name, "ошибка", err)
		writeServiceError(w, http.StatusUnauthorized, "секрет "+kind+" не задан")
		return "", false
	}
	if strings.TrimSpace(v) == "" {
		writeServiceError(w, http.StatusUnauthorized, "секрет "+kind+" не задан")
		return "", false
	}
	return v, true
}

// endpointLimiter — скользящее окно запросов в минуту на сервис (rate_limit).
type endpointLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func (l *endpointLimiter) allow(key string, max int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = make(map[string][]time.Time)
	}
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= max {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}

// MountServices монтирует поверхность HTTP-сервисов. Регистрируется на верхнем
// уровне роутера (вне session-middleware веб-интерфейса): каждый сервис сам
// объявляет свою аутентификацию (none/basic/session), поэтому публичные
// сервисы-приёмники вебхуков работают без cookie. Диспетчер читает реестр
// «вживую», поэтому --watch подхватывает новые сервисы без рестарта.
func (s *Server) MountServices(r chi.Router) {
	r.Handle("/hs", http.HandlerFunc(s.serviceIndex))
	r.Handle("/hs/", http.HandlerFunc(s.serviceIndex))
	// Статические маршруты приоритетнее catch-all /hs/* в chi.
	r.Get("/hs/docs", s.serviceDocs)
	r.Get("/hs/docs/rapidoc-min.js", s.serviceDocsAsset)
	r.Handle("/hs/*", http.HandlerFunc(s.serviceDispatch))
}

// serviceIndex — GET /hs: машиночитаемый список опубликованных сервисов
// (имя, заголовок, корневой URL, шаблоны и методы). Помогает интеграторам и
// заменяет ?wsdl-дискавери из мира SOAP.
func (s *Server) serviceIndex(w http.ResponseWriter, r *http.Request) {
	type tmplInfo struct {
		Template string   `json:"template"`
		Methods  []string `json:"methods"`
	}
	type svcInfo struct {
		Name      string     `json:"name"`
		Title     string     `json:"title,omitempty"`
		RootURL   string     `json:"root_url"`
		BaseURL   string     `json:"base_url"`
		Auth      string     `json:"auth"`
		Roles     []string   `json:"roles,omitempty"`
		Templates []tmplInfo `json:"templates"`
	}
	services := s.reg.HTTPServices()
	out := make([]svcInfo, 0, len(services))
	for _, svc := range services {
		info := svcInfo{Name: svc.Name, Title: svc.Title, RootURL: svc.RootURL, BaseURL: "/hs/" + svc.RootURL, Auth: svc.Auth, Roles: svc.Roles}
		for _, t := range svc.Templates {
			info.Templates = append(info.Templates, tmplInfo{Template: t.Template, Methods: sortedMethods(t.Methods)})
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RootURL < out[j].RootURL })
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	respondJSONTo(w, map[string]any{"services": out})
}

// serviceDispatch разбирает /hs/<корень>/<путь>, находит сервис и шаблон,
// проверяет метод и аутентификацию, затем выполняет DSL-обработчик.
func (s *Server) serviceDispatch(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/hs/")
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		s.serviceIndex(w, r)
		return
	}
	if rest == "openapi.json" {
		s.serviceOpenAPI(w, r)
		return
	}
	// Предохранитель сети (план 62): входящие сервисы — тоже сетевая
	// поверхность конфигурации. Дискавери (индекс/openapi) выше остаётся
	// доступным, а вызовы обработчиков — 503, пока сеть не разрешена.
	if !s.netEnabled(r.Context()) {
		writeServiceError(w, http.StatusServiceUnavailable, ErrNetworkLocked.Error())
		return
	}

	// План 90: входные шлюзы делят префикс /hs/ с сервисами. Их endpoint — полный
	// путь; при совпадении обслуживаем как приёмник (реестр читается вживую).
	if s.dispatchIntake(w, r) {
		return
	}

	root, remainder, _ := strings.Cut(rest, "/")
	svc := s.reg.GetHTTPServiceByRoot(root)
	if svc == nil {
		writeServiceError(w, http.StatusNotFound, "HTTP-сервис не найден: "+root)
		return
	}

	// CORS уровня сервиса. Заголовки Allow-Origin ставим на все ответы сервиса,
	// а preflight (OPTIONS) обрабатываем здесь же, не запуская обработчик.
	if svc.CORS != nil {
		s.setCORSHeaders(w, r, svc)
		if r.Method == http.MethodOptions {
			s.writeCORSPreflight(w, r, svc, remainder)
			return
		}
	}

	// CSRF-эквивалент для /hs/* (глобальный CSRF с сервисов снят, см.
	// api.csrfExceptServices): мутирующий запрос с чужим Origin исполняем,
	// только если источник явно разрешён CORS-политикой сервиса. Иначе —
	// браузерная межсайтовая атака (cookie session/basic) отклоняется.
	if isMutatingMethod(r.Method) {
		if origin := r.Header.Get("Origin"); origin != "" && !sameOriginHost(origin, r.Host) {
			allowed := false
			if svc.CORS != nil {
				if _, _, ok := matchOrigin(svc.CORS.Origins, origin); ok {
					allowed = true
				}
			}
			if !allowed {
				writeServiceError(w, http.StatusForbidden, "cross-origin запрос отклонён")
				return
			}
		}
	}

	tmpl, pathParams, ok := svc.Match("/" + remainder)
	if !ok {
		writeServiceError(w, http.StatusNotFound, "ресурс не найден: /"+remainder)
		return
	}
	handlerName, ok := tmpl.Methods[strings.ToUpper(r.Method)]
	if !ok {
		w.Header().Set("Allow", strings.Join(sortedMethods(tmpl.Methods), ", "))
		writeServiceError(w, http.StatusMethodNotAllowed, "метод не поддержан: "+r.Method)
		return
	}

	// Rate-limit уровня сервиса (поглощено из плана 58): защищает публичные
	// приёмники вебхуков от спама и cost-DoS. Ключуем по (сервис, IP клиента),
	// иначе один отправитель флудом исчерпал бы общую квоту и заблокировал
	// легитимные вебхуки остальных.
	if svc.RateLimit > 0 && !s.endpointLimit.allow(svc.RootURL+"|"+clientIP(r), svc.RateLimit) {
		w.Header().Set("Retry-After", "60")
		writeServiceError(w, http.StatusTooManyRequests, "превышен лимит запросов")
		return
	}

	// Тело читаем целиком ДО аутентификации (ограничено maxFileSizeBytes):
	// auth hmac проверяет подпись тела, а обработчик получает его как
	// байты/строку без возни с потоком.
	var body []byte
	if r.Body != nil {
		var rerr error
		body, rerr = io.ReadAll(http.MaxBytesReader(w, r.Body, s.maxFileSizeBytes))
		closeRead("тело запроса", r.Body)
		if rerr != nil {
			// Тело больше лимита нельзя молча усекать: hmac посчитал бы подпись
			// по огрызку (ложный 401), а обработчик обработал бы битые данные.
			var maxErr *http.MaxBytesError
			if errors.As(rerr, &maxErr) {
				writeServiceError(w, http.StatusRequestEntityTooLarge, "тело запроса превышает допустимый размер")
			} else {
				writeServiceError(w, http.StatusBadRequest, "не удалось прочитать тело запроса")
			}
			return
		}
	}

	ctx, ok := s.resolveServiceAuth(svc, w, r, body)
	if !ok {
		return // 401 уже отправлен
	}

	// Авторизация по ролям (план 61). Непустой roles: требует
	// аутентифицированного пользователя с одной из ролей (админ — всегда).
	if len(svc.Roles) > 0 {
		u := auth.UserFromContext(ctx)
		if u == nil || !u.HasAnyRole(svc.Roles) {
			writeServiceError(w, http.StatusForbidden, "доступ запрещён: требуется роль "+strings.Join(svc.Roles, "/"))
			return
		}
	}

	procDecl := s.reg.GetServiceProcedure(svc.Name, handlerName)
	if procDecl == nil {
		writeServiceError(w, http.StatusInternalServerError,
			"обработчик "+handlerName+" не найден в src/"+strings.ToLower(svc.Name)+".service.os")
		return
	}

	reqObj := interpreter.NewServiceRequest(r.Method, svc.RootURL, r.URL.Path, pathParams, r.URL.Query(), r.Header, body)

	opCtx, finish, ok := s.beginOperation(r, opHTTPServiceRun, svc.Name+"."+handlerName)
	if !ok {
		writeServiceError(w, http.StatusTooManyRequests, "слишком много одновременно выполняемых HTTP-сервисов, повторите позже")
		return
	}
	ctx = auth.ContextWithUser(opCtx, auth.UserFromContext(ctx))
	opStatus := "ok"
	defer func() { finish(opStatus, 0, false) }()

	var msgs []string
	mc := runtime.NewMovementsCollector("service", uuid.Nil)
	dslVars, txState := s.buildDSLVarsWithMessagesTx(ctx, mc, &msgs)
	defer rollbackDSLExecution(txState)
	// Запрос доступен и как параметр обработчика, и как глобальная переменная —
	// чтобы работали оба стиля: Функция H(Запрос) и Функция H() с чтением Запрос.
	dslVars["Запрос"] = reqObj
	dslVars["Request"] = reqObj

	var result any
	var err error
	if timeout := s.operationTimeout(opHTTPServiceRun); timeout > 0 {
		result, err = s.interp.CallSandboxed(procDecl, reqObj, []any{reqObj}, interpreter.SandboxProfile{Context: ctx, MaxWallClock: timeout}, dslVars)
	} else {
		result, err = s.interp.Call(procDecl, reqObj, []any{reqObj}, dslVars)
	}
	err = finishDSLExecution(txState, err)
	if err != nil {
		opStatus = operationStatus(opCtx, err)
		writeServiceError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeServiceResult(w, result)
}

// resolveServiceAuth применяет аутентификацию конкретного сервиса. body нужен
// режиму hmac (подпись тела). Возвращает контекст (с вложенным пользователем
// при успехе) и ok=false, если запрос уже отклонён (401 отправлен).
func (s *Server) resolveServiceAuth(svc *httpservice.Service, w http.ResponseWriter, r *http.Request, body []byte) (ctxOut context.Context, ok bool) {
	switch strings.ToLower(strings.TrimSpace(svc.Auth)) {
	case "", "none":
		return r.Context(), true

	case "token":
		// Постоянный секрет в заголовке — простой режим для вебхуков
		// (поглощено из плана 58). Сравнение constant-time.
		secret, ok := resolveAuthSecret(svc.Secret, "сервиса", svc.Name, w)
		if !ok {
			return nil, false
		}
		got := r.Header.Get("X-Webhook-Token")
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
			writeServiceError(w, http.StatusUnauthorized, "неверный токен")
			return nil, false
		}
		return r.Context(), true

	case "hmac":
		// Подпись запроса. Новая (versioned) схема с freshness/replay и старая
		// схема «подпись только тела» — в verifyServiceHMAC (#785).
		secret, ok := resolveAuthSecret(svc.Secret, "сервиса", svc.Name, w)
		if !ok {
			return nil, false
		}
		if ok, reason := verifyServiceHMAC(secret, svc.Name, r, body, serviceReplay); !ok {
			writeServiceError(w, http.StatusUnauthorized, reason)
			return nil, false
		}
		return r.Context(), true

	case "basic":
		login, pass, has := r.BasicAuth()
		if !has || s.authRepo == nil {
			return s.denyBasic(w)
		}
		// Брутфорс-защита: тот же лимитер попыток, что у формы входа. Без него
		// поток Authorization: Basic перебирал бы пароль без блокировки (+ cost-DoS
		// на bcrypt). Ключ — (IP, логин), как у /login.
		limKey := clientIP(r) + "|" + strings.ToLower(strings.TrimSpace(login))
		if ok, retry := s.loginLimit.Allow(limKey); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
			writeServiceError(w, http.StatusTooManyRequests, "слишком много попыток входа, повторите позже")
			return nil, false
		}
		// Политика входа действует и здесь. Иначе `auth: basic` был бы дырой
		// мимо неё: при sso_only локальный пароль обязан отклоняться, а учётке
		// с обязательным вторым фактором одного пароля недостаточно — код
		// сервиса исполнялся бы под личностью и ролями администратора
		// (ТекущийПользователь, аудит, RLS) по одному утёкшему паролю.
		// Исключение планом 84 выдавалось API-токенам (план 26), не паролю.
		policy := s.authRepo.AuthPolicy(r.Context())
		if !policy.PasswordLoginAllowed() {
			writeServiceError(w, http.StatusUnauthorized,
				"вход по паролю отключён политикой; используйте API-токен")
			return nil, false
		}
		u, err := s.authRepo.Authenticate(r.Context(), login, pass)
		if err != nil {
			s.loginLimit.Fail(limKey)
			return s.denyBasic(w)
		}
		// Второй фактор в потоке Basic предъявить негде, поэтому учётка, которой
		// он положен, паролем здесь не входит вовсе. Ошибку чтения признака
		// трактуем как «фактор есть»: путь fail-closed.
		if u != nil {
			enabled, totpErr := s.authRepo.TOTPEnabled(r.Context(), u.ID)
			if totpErr != nil || enabled || s.authRepo.RequiresTwoFactor(r.Context(), policy, u) {
				writeServiceError(w, http.StatusUnauthorized,
					"учётной записи требуется второй фактор; используйте API-токен")
				return nil, false
			}
		}
		s.loginLimit.Reset(limKey)
		return s.serviceUserCtx(r.Context(), u), true

	case "session":
		if s.authRepo == nil {
			writeServiceError(w, http.StatusUnauthorized, "требуется аутентификация")
			return nil, false
		}
		token := sessionToken(r)
		if token == "" {
			writeServiceError(w, http.StatusUnauthorized, "требуется аутентификация")
			return nil, false
		}
		u, err := s.authRepo.LookupSessionKind(r.Context(), token, auth.SessionKindEnterprise)
		if err != nil {
			writeServiceError(w, http.StatusUnauthorized, "недействительная сессия")
			return nil, false
		}
		return s.serviceUserCtx(r.Context(), u), true

	default:
		// Неизвестный режим — это ошибка конфигурации; не открываем доступ молча.
		writeServiceError(w, http.StatusInternalServerError, "неизвестный режим аутентификации сервиса: "+svc.Auth)
		return nil, false
	}
}

func (s *Server) denyBasic(w http.ResponseWriter) (context.Context, bool) {
	w.Header().Set("WWW-Authenticate", `Basic realm="onebase"`)
	writeServiceError(w, http.StatusUnauthorized, "требуется аутентификация")
	return nil, false
}

// serviceUserCtx вкладывает пользователя в контекст так, чтобы ТекущийПользователь()
// и аудит записи видели вызывающего (как делает session-middleware веб-интерфейса).
func (s *Server) serviceUserCtx(ctx context.Context, u *auth.User) context.Context {
	if roles, err := s.authRepo.GetRolesForUser(ctx, u.ID); err == nil {
		u.Roles = roles
	}
	ctx = auth.ContextWithUser(ctx, u)
	ctx = storage.WithAuditUser(ctx, u.ID, u.Login)
	return ctx
}

// writeServiceResult сериализует результат обработчика в HTTP-ответ. Кроме
// явного HTTPСервисОтвет поддержаны «голые» возвраты (удобство сверх 1С):
// строка → text/plain, Структура/Соответствие/Массив/число → JSON, Неопределено → 204.
func (s *Server) writeServiceResult(w http.ResponseWriter, result any) {
	switch v := result.(type) {
	case *interpreter.DSLServiceResponse:
		for k, val := range v.HeadersMap() {
			w.Header().Set(k, val)
		}
		code := v.StatusCodeValue()
		if code == 0 {
			code = http.StatusOK
		}
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
		w.WriteHeader(code)
		_, _ = w.Write(v.BodyBytes()) //nolint:gosec // G705: ответ уходит с инертным типом (text/plain или application/json), браузер его не исполняет
	case nil:
		w.WriteHeader(http.StatusNoContent)
	case string:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(v)) //nolint:gosec // G705: ответ уходит с инертным типом (text/plain или application/json), браузер его не исполняет
	default:
		data, err := interpreter.MarshalDSLValue(v)
		if err != nil {
			writeServiceError(w, http.StatusInternalServerError, "сериализация ответа: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(data) //nolint:gosec // G705: ответ уходит с инертным типом (text/plain или application/json), браузер его не исполняет
	}
}

func writeServiceError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	respondJSONTo(w, map[string]any{"error": msg})
}

// sessionToken читает токен сессии ТОЛЬКО из cookie onebase_session. Приём из
// query-параметра _tk убран (план 53): токен в URL утекает в историю браузера,
// Referer и access-логи прокси. Это совпадает с auth.Middleware, где _tk тоже
// больше не принимается.
func sessionToken(r *http.Request) string {
	if c, err := r.Cookie("onebase_session"); err == nil && c.Value != "" {
		return c.Value
	}
	return ""
}

// clientIP возвращает IP-адрес клиента из RemoteAddr (без порта). X-Forwarded-For
// намеренно не используется — без доверенного прокси заголовок подделывается и
// позволил бы обходить лимиты или блокировать чужие IP.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// isMutatingMethod — методы, для которых проверяем cross-origin (как CSRFProtect).
func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// sameOriginHost сравнивает host:port из Origin с Host запроса.
func sameOriginHost(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, host)
}

// setCORSHeaders ставит Access-Control-Allow-Origin (+ credentials) согласно
// политике сервиса. С credentials нельзя отвечать «*» — эхо конкретного источника.
func (s *Server) setCORSHeaders(w http.ResponseWriter, r *http.Request, svc *httpservice.Service) {
	origin := r.Header.Get("Origin")
	allow, wildcard, ok := matchOrigin(svc.CORS.Origins, origin)
	if !ok {
		return
	}
	if svc.CORS.Credentials {
		// Учётные данные (cookie session/basic) с CORS требуют ЯВНОГО источника.
		// Если Origin совпал только по «*», credentialed CORS НЕ выдаём: иначе
		// любой сайт прочитал бы ответ сервиса с куками жертвы (отражение
		// произвольного Origin + Allow-Credentials: true).
		if wildcard {
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	} else {
		w.Header().Set("Access-Control-Allow-Origin", allow)
	}
	w.Header().Add("Vary", "Origin")
}

// writeCORSPreflight отвечает на OPTIONS-preflight: разрешённые методы (из
// шаблона ресурса либо объединение всех), заголовки и время кэша.
func (s *Server) writeCORSPreflight(w http.ResponseWriter, r *http.Request, svc *httpservice.Service, remainder string) {
	var methods []string
	if t, _, ok := svc.Match("/" + remainder); ok {
		methods = sortedMethods(t.Methods)
	} else {
		set := map[string]string{}
		for _, t := range svc.Templates {
			for m := range t.Methods {
				set[m] = ""
			}
		}
		methods = sortedMethods(set)
	}
	methods = append(methods, http.MethodOptions)
	w.Header().Set("Access-Control-Allow-Methods", strings.Join(methods, ", "))

	if len(svc.CORS.Headers) > 0 {
		w.Header().Set("Access-Control-Allow-Headers", strings.Join(svc.CORS.Headers, ", "))
	} else if reqH := r.Header.Get("Access-Control-Request-Headers"); reqH != "" {
		w.Header().Set("Access-Control-Allow-Headers", reqH)
	}
	if svc.CORS.MaxAge > 0 {
		w.Header().Set("Access-Control-Max-Age", strconv.Itoa(svc.CORS.MaxAge))
	}
	w.WriteHeader(http.StatusNoContent)
}

// matchOrigin возвращает значение для Allow-Origin, признак совпадения по
// wildcard «*» и признак совпадения вообще. Явное совпадение источника имеет
// приоритет над «*» (важно для credentialed CORS, где «*» недопустим).
func matchOrigin(origins []string, origin string) (allow string, wildcard bool, ok bool) {
	hasWildcard := false
	for _, o := range origins {
		if o == "*" {
			hasWildcard = true
			continue
		}
		if origin != "" && strings.EqualFold(o, origin) {
			return origin, false, true
		}
	}
	if hasWildcard {
		return "*", true, true
	}
	return "", false, false
}

func sortedMethods(methods map[string]string) []string {
	out := make([]string, 0, len(methods))
	for m := range methods {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}
