package auth

import (
	"context"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	oblog "github.com/ivantit66/onebase/internal/logging"
)

func authLog() *slog.Logger { return oblog.Component("auth") }

// maxLoginFormBytes — предел тела формы входа. Логин и пароль — короткие поля;
// запас на порядки больше любого разумного значения и при этом конечен.
const maxLoginFormBytes = int64(64 << 10)

// maxCredentialLen — верхний предел длины логина и пароля. bcrypt всё равно
// использует лишь первые 72 байта пароля, а логин — короткое поле; ограничение
// отсекает патологически длинные значения до хеширования (issue #776).
const maxCredentialLen = 1024

// credentialsTooLong — общая отсечка длины для ОБОИХ входов.
//
// Раньше проверка стояла только в LoginJSON: у формы входа предел был лишь на
// тело (64 КиБ), то есть пара «логин 60 КиБ / пароль 60 КиБ» доезжала до ключа
// rate-limiter'а и до Authenticate — тот же вектор на соседнем публичном
// маршруте (#864). Предел один на оба входа намеренно: разъехавшись, они снова
// закроют разное.
func credentialsTooLong(login, password string) bool {
	return len(login) > maxCredentialLen || len(password) > maxCredentialLen
}

var loginTmpl = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html lang="ru">
<head><meta charset="utf-8"><title>Вход — onebase</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:'Segoe UI',Arial,sans-serif;background:#f0f0f0;display:flex;align-items:center;justify-content:center;height:100vh}
.box{background:#fff;padding:32px 40px;border:1px solid #ccc;border-radius:4px;width:340px;box-shadow:0 2px 8px rgba(0,0,0,.15)}
h2{margin:0 0 24px;color:#1a5fa8;font-size:18px;font-weight:600}
label{display:block;font-size:13px;margin-bottom:4px;color:#333;font-weight:500}
input,select{width:100%;padding:8px 10px;border:1px solid #bbb;border-radius:3px;font-size:14px;margin-bottom:16px;outline:none;background:#fff}
input:focus,select:focus{border-color:#1a5fa8;box-shadow:0 0 0 2px rgba(26,95,168,.15)}
.btn{width:100%;background:#1a5fa8;color:#fff;border:none;padding:10px;font-size:14px;border-radius:3px;cursor:pointer;font-weight:500}
.btn:hover{background:#1550a0}
.err{color:#c00;font-size:13px;margin-bottom:14px;padding:8px;background:#fff0f0;border-radius:3px;border:1px solid #fcc}
.sep{display:flex;align-items:center;gap:8px;color:#94a3b8;font-size:12px;margin:4px 0 14px}
.sep:before,.sep:after{content:"";flex:1;height:1px;background:#e2e8f0}
.btn-sso{display:block;text-align:center;text-decoration:none;background:#fff;color:#1a5fa8;border:1px solid #1a5fa8;margin-bottom:8px;line-height:20px}
.btn-sso:hover{background:#f0f6ff}
</style></head>
<body>
<div class="box">
  <h2>⚡ onebase — Вход</h2>
  {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
  {{if .PasswordLogin}}
  <form method="POST">
    <label>Имя пользователя</label>
    <input name="login" autofocus autocomplete="off" {{if .Users}}list="ob-users"{{end}}>
    {{if .Users}}<datalist id="ob-users">{{range .Users}}<option value="{{.Login}}">{{if .FullName}}{{.FullName}}{{end}}</option>{{end}}</datalist>{{end}}
    <label>Пароль</label>
    <input name="password" type="password" autocomplete="current-password">
    <button class="btn" type="submit">Войти</button>
  </form>
  {{end}}
  {{if .Providers}}
  {{if .PasswordLogin}}<div class="sep"><span>или</span></div>{{end}}
  {{range .Providers}}<a class="btn btn-sso" href="/auth/oidc/{{.ID}}/start{{$.ReturnQuery}}">Войти через {{.Name}}</a>{{end}}
  {{end}}
  {{if and (not .PasswordLogin) (not .Providers)}}
  <div class="err">Вход по паролю запрещён политикой, а провайдеры единого входа не настроены. Обратитесь к администратору базы.</div>
  {{end}}
</div>
</body></html>`))

// AuditLogger is implemented by storage.DB to log auth events.
type AuditLogger interface {
	LogAction(ctx context.Context, action, kind, entityName, recordID, userID, userLogin, ip string)
}

type Handlers struct {
	Repo          *Repo
	Auditor       AuditLogger   // optional, set by api.New
	Codes         *OneTimeCodes // одноразовые bootstrap-коды (план 53); optional
	LoginLimit    *LoginLimiter // rate-limit попыток входа (план 53); optional
	SecureCookies bool          // force Secure behind a trusted HTTPS terminator
	// Challenges — незавершённые входы, ожидающие второго фактора (план 84).
	// nil → общее хранилище процесса (DefaultChallenges).
	Challenges *Challenges
	// OIDC — клиент внешних провайдеров входа (план 84). nil → общий клиент
	// процесса (DefaultOIDCClient).
	OIDC *OIDCClient
	// AppName — имя базы в приложении-аутентификаторе и в otpauth-ссылке.
	AppName string
	// BaseURL — внешний адрес базы для redirect_uri провайдера
	// (https://erp.example.com). Пусто → адрес собирается из запроса.
	BaseURL string
}

// sessionMetaFromRequest собирает мету сессии пользовательского режима.
func sessionMetaFromRequest(r *http.Request) SessionMeta {
	return SessionMeta{Kind: SessionKindEnterprise, IP: r.RemoteAddr, UserAgent: r.UserAgent()}
}

func (h *Handlers) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "onebase_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.SecureCookies || r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}

// limitExceeded отвечает 429 + Retry-After, если ключ заблокирован лимитером.
func (h *Handlers) limitExceeded(w http.ResponseWriter, r *http.Request, login string) bool {
	if h.LoginLimit == nil {
		return false
	}
	ok, retry := h.LoginLimit.Allow(LoginKey(r, login))
	if ok {
		return false
	}
	w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
	http.Error(w, "Слишком много попыток входа — повторите позже", http.StatusTooManyRequests)
	return true
}

// IssueOneTimeCode handles POST /auth/one-time-code: выдаёт короткоживущий
// одноразовый код для текущей сессии (cookie). Конфигуратор обменивает его
// через /auth/bootstrap?code=... — сессионный токен не попадает в URL.
func (h *Handlers) IssueOneTimeCode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.Codes == nil {
		http.Error(w, `{"error":"one-time codes disabled"}`, http.StatusNotFound)
		return
	}
	cookie, err := r.Cookie("onebase_session")
	if err != nil || cookie.Value == "" {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	user, err := h.Repo.LookupSessionKind(r.Context(), cookie.Value, SessionKindConfigurator)
	if err != nil || user == nil || !user.IsAdmin {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	code, err := h.Codes.Issue(cookie.Value)
	if err != nil {
		h.internalErrorJSON(w, r, "выдача одноразового кода", err)
		return
	}
	respondJSONTo(w, map[string]any{"code": code})
}

// loginPageData собирает данные формы входа: список пользователей для подсказки,
// кнопки внешних провайдеров и признак того, разрешён ли вход по паролю
// (политика sso_only, план 84).
func (h *Handlers) loginPageData(r *http.Request, errMsg string) map[string]any {
	var users []*User
	var providers []*OIDCProvider
	passwordLogin := true
	if h.Repo != nil {
		users, _ = h.Repo.ListForSelection(r.Context())
		providers = h.Repo.EnabledAuthProviders(r.Context())
		passwordLogin = h.Repo.AuthPolicy(r.Context()).PasswordLoginAllowed()
	}
	returnQuery := ""
	if ret := r.URL.Query().Get("return"); ret != "" && isLocalURL(ret) {
		returnQuery = "?return=" + url.QueryEscape(ret)
	}
	return map[string]any{
		"Error":         errMsg,
		"Users":         users,
		"Providers":     providers,
		"PasswordLogin": passwordLogin,
		"ReturnQuery":   returnQuery,
	}
}

func (h *Handlers) LoginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	errMsg := ""
	switch r.URL.Query().Get("err") {
	case "2fa":
		errMsg = "Слишком много неверных кодов подтверждения — начните вход заново"
	case "sso":
		errMsg = "Единый вход не удался — подробности в журнале сервера"
	case "sso_user":
		errMsg = "Учётная запись не найдена в этой базе — обратитесь к администратору"
	}
	renderTemplate(w, loginTmpl, h.loginPageData(r, errMsg))
}

func (h *Handlers) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	renderErr := func(w http.ResponseWriter, r *http.Request, code int, msg string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(code)
		renderTemplate(w, loginTmpl, h.loginPageData(r, msg))
	}

	// Сбой разбора формы нельзя пропускать: дальше login и password окажутся
	// пустыми, и попытка входа уйдёт в общий путь «неверный логин или пароль».
	// Пользователь будет искать ошибку в своих учётных данных, а дело в теле
	// запроса.
	//
	// Тело ограничиваем здесь же: голый ParseForm читает его целиком, а форма
	// входа доступна без аутентификации — то есть это единственная точка, куда
	// неаутентифицированный клиент может слать сколько угодно данных.
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginFormBytes)
	if err := r.ParseForm(); err != nil {
		renderErr(w, r, http.StatusBadRequest, "некорректные данные формы")
		return
	}
	login := r.FormValue("login")
	password := r.FormValue("password")

	// Отсечка до лимитера и до Authenticate: длинное значение не должно ни
	// становиться ключом rate-limiter'а, ни доезжать до сравнения хэшей.
	if credentialsTooLong(login, password) {
		renderErr(w, r, http.StatusBadRequest, "некорректные данные формы")
		return
	}

	if h.limitExceeded(w, r, login) {
		return
	}

	// Политика читается ДО проверки пароля: при sso_only локальный пароль не
	// принимается вовсе, и незачем даже сверять его с хэшем.
	policy := h.Repo.AuthPolicy(r.Context())
	if !policy.PasswordLoginAllowed() {
		renderErr(w, r, http.StatusForbidden, "Вход по паролю запрещён политикой базы — используйте единый вход")
		return
	}

	user, err := h.Repo.Authenticate(r.Context(), login, password)
	if err != nil {
		if h.LoginLimit != nil {
			h.LoginLimit.Fail(LoginKey(r, login))
		}
		renderErr(w, r, http.StatusUnauthorized, "Неверное имя пользователя или пароль")
		return
	}

	returnURL := r.URL.Query().Get("return")
	if returnURL == "" || !isLocalURL(returnURL) {
		returnURL = "/ui"
	}

	// Второй фактор (план 84). Сессия не создаётся, пока код не предъявлен;
	// счётчик неудачных попыток входа тоже не сбрасывается — пароль ещё не
	// довёл до сессии.
	switch enabled, terr := h.Repo.TOTPEnabled(r.Context(), user.ID); {
	case terr != nil:
		authLog().Error("не удалось проверить состояние 2FA", "логин", user.Login, "err", terr)
		renderErr(w, r, http.StatusServiceUnavailable, "Служба аутентификации временно недоступна")
		return
	case enabled:
		h.beginSecondFactor(w, r, user, false, false, returnURL)
		return
	case h.Repo.RequiresTwoFactor(r.Context(), policy, user):
		// Привязка на входе по одному паролю разрешена только явной политикой
		// (issue #577) — иначе сперва потребуется код привязки от администратора.
		h.beginSecondFactor(w, r, user, true, policy.SelfEnroll2FA, returnURL)
		return
	}

	if h.LoginLimit != nil {
		h.LoginLimit.Reset(LoginKey(r, login))
	}

	token, err := h.Repo.CreateSession(r.Context(), user.ID, sessionMetaFromRequest(r))
	if err != nil {
		h.internalError(w, r, "создание сессии", err)
		return
	}

	if h.Auditor != nil {
		ip := r.RemoteAddr
		h.Auditor.LogAction(r.Context(), "login", "", "", "", user.ID, user.Login, ip)
	}

	h.setSessionCookie(w, r, token)
	http.Redirect(w, r, returnURL, http.StatusFound)
}

func (h *Handlers) LoginJSON(w http.ResponseWriter, r *http.Request) {
	// Публичный неаутентифицированный маршрут: тело ограничиваем тем же
	// пределом, что и форму входа. Без этого клиент мог слать сколь угодно
	// большое тело и заставлять сервер аллоцировать под него память (issue #776).
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginFormBytes)
	var req struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	if credentialsTooLong(req.Login, req.Password) {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}

	if h.limitExceeded(w, r, req.Login) {
		return
	}

	policy := h.Repo.AuthPolicy(r.Context())
	if !policy.PasswordLoginAllowed() {
		http.Error(w, `{"error":"password login disabled"}`, http.StatusForbidden)
		return
	}

	user, err := h.Repo.Authenticate(r.Context(), req.Login, req.Password)
	if err != nil {
		if h.LoginLimit != nil {
			h.LoginLimit.Fail(LoginKey(r, req.Login))
		}
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}

	// Второй фактор: сессии ещё нет, клиент получает challenge и досылает код
	// на /auth/2fa. Первичную настройку JSON-клиенту предложить нечем — она
	// требует QR и резервных кодов, поэтому такой ответ ведёт в веб-интерфейс.
	switch enabled, terr := h.Repo.TOTPEnabled(r.Context(), user.ID); {
	case terr != nil:
		// 503, а не 500: состояние 2FA временно недоступно. Причину всё равно
		// пишем — молчащий отказ входа разбирать нечем (#1053).
		logInternalError(r, "проверка состояния 2FA (JSON-вход)", terr)
		http.Error(w, `{"error":"internal"}`, http.StatusServiceUnavailable)
		return
	case enabled, h.Repo.RequiresTwoFactor(r.Context(), policy, user):
		ch := Challenge{UserID: user.ID, Login: user.Login, Enroll: !enabled}
		if ch.Enroll {
			secret, serr := GenerateTOTPSecret()
			if serr != nil {
				h.internalErrorJSON(w, r, "генерация секрета 2FA", serr)
				return
			}
			ch.Secret = secret
		}
		token, cerr := h.challenges().Issue(ch)
		if cerr != nil {
			h.internalErrorJSON(w, r, "выдача challenge второго фактора", cerr)
			return
		}
		h.setChallengeCookie(w, r, token)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		reason := "2fa_required"
		if ch.Enroll {
			reason = "2fa_setup_required"
		}
		respondJSONTo(w, map[string]any{"error": reason, "challenge": token})
		return
	}

	if h.LoginLimit != nil {
		h.LoginLimit.Reset(LoginKey(r, req.Login))
	}

	token, err := h.Repo.CreateSession(r.Context(), user.ID, sessionMetaFromRequest(r))
	if err != nil {
		h.internalErrorJSON(w, r, "создание сессии (JSON-вход)", err)
		return
	}

	if h.Auditor != nil {
		h.Auditor.LogAction(r.Context(), "login", "", "", "", user.ID, user.Login, r.RemoteAddr)
	}

	h.setSessionCookie(w, r, token)
	w.Header().Set("Content-Type", "application/json")
	respondJSONTo(w, map[string]any{
		"ok":   true,
		"user": map[string]any{"id": user.ID, "login": user.Login, "is_admin": user.IsAdmin},
	})
}

func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("onebase_session"); err == nil {
		if h.Auditor != nil {
			if user, err2 := h.Repo.LookupSessionKind(r.Context(), cookie.Value, SessionKindEnterprise); err2 == nil {
				h.Auditor.LogAction(r.Context(), "logout", "", "", "", user.ID, user.Login, r.RemoteAddr)
			}
		}
		// Сбой удаления — вопрос безопасности, а не уборки: cookie у клиента мы
		// сотрём в любом случае, но серверная сессия останется валидной, и
		// перехваченный токен продолжит работать до истечения TTL. Отказать в
		// выходе нельзя (пользователь останется залогинен), поэтому громко пишем
		// в лог — это единственное, что отличает такой случай от штатного выхода.
		if err := h.Repo.DeleteSession(r.Context(), cookie.Value); err != nil {
			authLog().Error("не удалось удалить сессию при выходе — токен остаётся валидным до истечения TTL",
				"err", err)
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "onebase_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.SecureCookies || r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (h *Handlers) Status(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	hasUsers, err := h.Repo.HasUsers(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		respondJSONTo(w, map[string]string{"error": "authentication service unavailable"})
		return
	}
	respondJSONTo(w, map[string]any{"requires_auth": hasUsers})
}

// Bootstrap sets the session cookie from a one-time code and redirects.
// Used by the launcher to pass the session into a new browser window.
// Optional "return" query param specifies the redirect target (default: /ui).
// Сырой сессионный токен в query НЕ принимается: токен в URL утекает в логи,
// Referer и историю браузера (план 53, этап 1) — только одноразовый код,
// выданный IssueOneTimeCode (single-use, короткий TTL).
func (h *Handlers) Bootstrap(w http.ResponseWriter, r *http.Request) {
	returnURL := r.URL.Query().Get("return")
	if returnURL == "" || !isLocalURL(returnURL) {
		returnURL = "/ui"
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Redirect(w, r, returnURL, http.StatusFound)
		return
	}
	if h.Codes == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	configuratorToken, ok := h.Codes.Exchange(code)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	user, err := h.Repo.LookupSessionKind(r.Context(), configuratorToken, SessionKindConfigurator)
	if err != nil || user == nil || !user.IsAdmin {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	// Crossing from the configurator to Enterprise is an explicit privilege
	// transition. Issue a separate Enterprise session instead of relabelling or
	// reusing the configurator bearer token in the base browser origin.
	token, err := h.Repo.CreateSession(r.Context(), user.ID, sessionMetaFromRequest(r))
	if err != nil {
		h.internalError(w, r, "создание сессии Предприятия из конфигуратора", err)
		return
	}
	h.setSessionCookie(w, r, token)
	http.Redirect(w, r, returnURL, http.StatusFound)
}

func isLocalURL(s string) bool {
	// Must start with '/' but not '//' (protocol-relative URLs like //evil.com are unsafe)
	return len(s) > 0 && s[0] == '/' && (len(s) < 2 || s[1] != '/')
}
