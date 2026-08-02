package auth

import (
	"context"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"

	oblog "github.com/ivantit66/onebase/internal/logging"
)

func authLog() *slog.Logger { return oblog.Component("auth") }

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
</style></head>
<body>
<div class="box">
  <h2>⚡ onebase — Вход</h2>
  {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
  <form method="POST">
    <label>Имя пользователя</label>
    <input name="login" autofocus autocomplete="off" {{if .Users}}list="ob-users"{{end}}>
    {{if .Users}}<datalist id="ob-users">{{range .Users}}<option value="{{.Login}}">{{if .FullName}}{{.FullName}}{{end}}</option>{{end}}</datalist>{{end}}
    <label>Пароль</label>
    <input name="password" type="password" autocomplete="current-password">
    <button class="btn" type="submit">Войти</button>
  </form>
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
	if _, err := h.Repo.LookupSession(r.Context(), cookie.Value); err != nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	code, err := h.Codes.Issue(cookie.Value)
	if err != nil {
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"code": code})
}

func (h *Handlers) LoginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var users []*User
	if h.Repo != nil {
		users, _ = h.Repo.ListForSelection(r.Context())
	}
	loginTmpl.Execute(w, map[string]any{"Error": "", "Users": users})
}

func (h *Handlers) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	renderErr := func(w http.ResponseWriter, r *http.Request, code int, msg string) {
		var users []*User
		if h.Repo != nil {
			users, _ = h.Repo.ListForSelection(r.Context())
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(code)
		loginTmpl.Execute(w, map[string]any{"Error": msg, "Users": users})
	}

	r.ParseForm()
	login := r.FormValue("login")
	password := r.FormValue("password")

	if h.limitExceeded(w, r, login) {
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
	if h.LoginLimit != nil {
		h.LoginLimit.Reset(LoginKey(r, login))
	}

	token, err := h.Repo.CreateSession(r.Context(), user.ID, sessionMetaFromRequest(r))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if h.Auditor != nil {
		ip := r.RemoteAddr
		h.Auditor.LogAction(r.Context(), "login", "", "", "", user.ID, user.Login, ip)
	}

	h.setSessionCookie(w, r, token)

	returnURL := r.URL.Query().Get("return")
	if returnURL == "" || !isLocalURL(returnURL) {
		returnURL = "/ui"
	}
	http.Redirect(w, r, returnURL, http.StatusFound)
}

func (h *Handlers) LoginJSON(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}

	if h.limitExceeded(w, r, req.Login) {
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
	if h.LoginLimit != nil {
		h.LoginLimit.Reset(LoginKey(r, req.Login))
	}

	token, err := h.Repo.CreateSession(r.Context(), user.ID, sessionMetaFromRequest(r))
	if err != nil {
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}

	if h.Auditor != nil {
		h.Auditor.LogAction(r.Context(), "login", "", "", "", user.ID, user.Login, r.RemoteAddr)
	}

	h.setSessionCookie(w, r, token)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":   true,
		"user": map[string]any{"id": user.ID, "login": user.Login, "is_admin": user.IsAdmin},
	})
}

func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("onebase_session"); err == nil {
		if h.Auditor != nil {
			if user, err2 := h.Repo.LookupSession(r.Context(), cookie.Value); err2 == nil {
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
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "authentication service unavailable"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"requires_auth": hasUsers})
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
	token, ok := h.Codes.Exchange(code)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	if _, err := h.Repo.LookupSession(r.Context(), token); err != nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	h.setSessionCookie(w, r, token)
	http.Redirect(w, r, returnURL, http.StatusFound)
}

func isLocalURL(s string) bool {
	// Must start with '/' but not '//' (protocol-relative URLs like //evil.com are unsafe)
	return len(s) > 0 && s[0] == '/' && (len(s) < 2 || s[1] != '/')
}
