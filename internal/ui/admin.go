package ui

import (
	"html/template"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
)

var adminTmpl = template.Must(template.New("admin").Parse(tplAdminUsers + tplAdminUserForm))

const tplAdminUsers = `{{define "admin-users"}}` + adminHead + `
<main>
<div class="row-top" style="max-width:700px">
  <h2>Пользователи</h2>
  <a class="btn btn-primary" href="/ui/admin/users/new">+ Добавить</a>
</div>
<div class="card" style="max-width:700px">
{{if .Users}}
<table>
<thead><tr>
  <th>Логин</th><th>Имя</th><th>Администратор</th><th>Создан</th><th style="width:90px"></th>
</tr></thead>
<tbody>
{{range .Users}}<tr>
  <td><strong>{{.Login}}</strong></td>
  <td>{{.FullName}}</td>
  <td>{{if .IsAdmin}}✓{{end}}</td>
  <td style="font-size:12px;color:#94a3b8">{{.CreatedAt.Format "02.01.2006"}}</td>
  <td>
    <form method="POST" action="/ui/admin/users/{{.ID}}/delete" onsubmit="return confirm('Удалить пользователя {{.Login}}?')">
      <button class="btn btn-sm btn-danger" type="submit">Удалить</button>
    </form>
  </td>
</tr>{{end}}
</tbody>
</table>
{{else}}
<p class="empty">Пользователей нет — вход в систему без пароля.<br>Добавьте пользователя, чтобы включить авторизацию.</p>
{{end}}
</div>
</main></body></html>
{{end}}`

const tplAdminUserForm = `{{define "admin-user-form"}}` + adminHead + `
<main>
<h2>Добавить пользователя</h2>
{{if .Error}}<div class="error" style="max-width:500px">{{.Error}}</div>{{end}}
<div class="card" style="max-width:500px">
<form method="POST">
  <div class="form-group">
    <label>Логин</label>
    <input type="text" name="login" required autofocus>
  </div>
  <div class="form-group">
    <label>Полное имя</label>
    <input type="text" name="full_name">
  </div>
  <div class="form-group">
    <label>Пароль</label>
    <input type="password" name="password" required>
  </div>
  <div class="form-group">
    <label style="display:flex;align-items:center;gap:8px;cursor:pointer">
      <input type="checkbox" name="is_admin" value="1"> Администратор
    </label>
  </div>
  <div style="display:flex;gap:12px;margin-top:8px">
    <button class="btn btn-primary" type="submit">Создать</button>
    <a class="btn" href="/ui/admin/users" style="background:#e2e8f0;color:#475569">Отмена</a>
  </div>
</form>
</div>
</main></body></html>
{{end}}`

const adminHead = `<!DOCTYPE html>
<html lang="ru"><head><meta charset="UTF-8"><title>Администрирование — onebase</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:system-ui,sans-serif;background:#f5f5f5;padding:32px}
h2{font-size:22px;font-weight:600;margin-bottom:20px;color:#1e293b}
.card{background:#fff;border-radius:10px;padding:24px;box-shadow:0 1px 3px rgba(0,0,0,.1)}
table{width:100%;border-collapse:collapse;font-size:14px}
th{text-align:left;padding:10px 12px;border-bottom:2px solid #e2e8f0;color:#64748b;font-weight:600}
td{padding:10px 12px;border-bottom:1px solid #f1f5f9;color:#334155}
tr:last-child td{border-bottom:none}
.btn{display:inline-block;padding:8px 18px;border-radius:7px;font-size:14px;font-weight:500;text-decoration:none;cursor:pointer;border:none}
.btn-primary{background:#3b82f6;color:#fff}.btn-primary:hover{background:#2563eb}
.btn-sm{padding:5px 12px;font-size:13px}
.btn-danger{background:#ef4444;color:#fff}.btn-danger:hover{background:#dc2626}
.form-group{margin-bottom:16px}
label{display:block;font-size:13px;font-weight:500;margin-bottom:5px;color:#475569}
input[type=text],input[type=password]{width:100%;padding:9px 12px;border:1px solid #e2e8f0;border-radius:7px;font-size:14px}
input:focus{border-color:#3b82f6;outline:none}
.error{background:#fef2f2;border:1px solid #fecaca;color:#dc2626;padding:12px;border-radius:7px;margin-bottom:16px;font-size:14px}
.empty{color:#94a3b8;text-align:center;padding:32px;font-size:14px}
.row-top{display:flex;justify-content:space-between;align-items:center;margin-bottom:16px}
</style></head><body>
<div style="margin-bottom:16px">
  <a href="/ui" style="color:#64748b;font-size:13px;text-decoration:none">← Главная</a>
</div>`

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	if s.authRepo == nil {
		http.Error(w, "auth not configured", 500)
		return
	}
	if !s.isAdmin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	users, err := s.authRepo.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	adminTmpl.ExecuteTemplate(w, "admin-users", map[string]any{"Users": users})
}

func (s *Server) adminUserNew(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	adminTmpl.ExecuteTemplate(w, "admin-user-form", map[string]any{"Error": ""})
}

func (s *Server) adminUserCreate(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	r.ParseForm()
	login := r.FormValue("login")
	password := r.FormValue("password")
	fullName := r.FormValue("full_name")
	isAdmin := r.FormValue("is_admin") == "1"

	if login == "" || password == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		adminTmpl.ExecuteTemplate(w, "admin-user-form", map[string]any{"Error": "Логин и пароль обязательны"})
		return
	}

	if _, err := s.authRepo.Create(r.Context(), login, password, fullName, isAdmin); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		adminTmpl.ExecuteTemplate(w, "admin-user-form", map[string]any{"Error": err.Error()})
		return
	}
	http.Redirect(w, r, "/ui/admin/users", http.StatusFound)
}

func (s *Server) adminUserDelete(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id := chi.URLParam(r, "id")
	s.authRepo.Delete(r.Context(), id)
	http.Redirect(w, r, "/ui/admin/users", http.StatusFound)
}

// isAdmin returns true if the current request has an admin user in context,
// or if no auth is configured (open access).
func (s *Server) isAdmin(r *http.Request) bool {
	if s.authRepo == nil {
		return true
	}
	hasUsers, err := s.authRepo.HasUsers(r.Context())
	if err != nil || !hasUsers {
		return true // no auth configured
	}
	u := auth.UserFromContext(r.Context())
	return u != nil && u.IsAdmin
}
