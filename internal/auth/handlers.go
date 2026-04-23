package auth

import (
	"encoding/json"
	"html/template"
	"net/http"
)

var loginTmpl = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html lang="ru">
<head><meta charset="utf-8"><title>Вход — onebase</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:'Segoe UI',Arial,sans-serif;background:#f0f0f0;display:flex;align-items:center;justify-content:center;height:100vh}
.box{background:#fff;padding:32px 40px;border:1px solid #ccc;border-radius:4px;width:340px;box-shadow:0 2px 8px rgba(0,0,0,.15)}
h2{margin:0 0 24px;color:#1a5fa8;font-size:18px;font-weight:600}
label{display:block;font-size:13px;margin-bottom:4px;color:#333;font-weight:500}
input{width:100%;padding:8px 10px;border:1px solid #bbb;border-radius:3px;font-size:14px;margin-bottom:16px;outline:none}
input:focus{border-color:#1a5fa8;box-shadow:0 0 0 2px rgba(26,95,168,.15)}
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
    <input name="login" autofocus autocomplete="username">
    <label>Пароль</label>
    <input name="password" type="password" autocomplete="current-password">
    <button class="btn" type="submit">Войти</button>
  </form>
</div>
</body></html>`))

type Handlers struct {
	Repo *Repo
}

func (h *Handlers) LoginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	loginTmpl.Execute(w, map[string]any{"Error": ""})
}

func (h *Handlers) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	login := r.FormValue("login")
	password := r.FormValue("password")

	user, err := h.Repo.Authenticate(r.Context(), login, password)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		loginTmpl.Execute(w, map[string]any{"Error": "Неверное имя пользователя или пароль"})
		return
	}

	token, err := h.Repo.CreateSession(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "onebase_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

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

	user, err := h.Repo.Authenticate(r.Context(), req.Login, req.Password)
	if err != nil {
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}

	token, err := h.Repo.CreateSession(r.Context(), user.ID)
	if err != nil {
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"token": token,
		"user":  map[string]any{"id": user.ID, "login": user.Login, "is_admin": user.IsAdmin},
	})
}

func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("onebase_session"); err == nil {
		h.Repo.DeleteSession(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:   "onebase_session",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (h *Handlers) Status(w http.ResponseWriter, r *http.Request) {
	hasUsers, _ := h.Repo.HasUsers(r.Context())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"requires_auth": hasUsers})
}

// Bootstrap sets session cookie from token param and redirects to /ui.
// Used by the launcher to pass the session into a new browser window.
func (h *Handlers) Bootstrap(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Redirect(w, r, "/ui", http.StatusFound)
		return
	}
	if _, err := h.Repo.LookupSession(r.Context(), token); err != nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "onebase_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/ui", http.StatusFound)
}

func isLocalURL(s string) bool {
	return len(s) > 0 && s[0] == '/'
}
