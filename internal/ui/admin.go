package ui

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/storage"
)

var adminTmpl = template.Must(template.New("admin").Parse(tplAdminUsers + tplAdminUserForm + tplAdminSessions + tplAdminCleanup + tplAdminRoles + tplAdminUserRoles + tplAdminAudit))

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
  <th>Логин</th><th>Имя</th><th>Администратор</th><th>Создан</th><th style="width:150px"></th>
</tr></thead>
<tbody>
{{range .Users}}<tr>
  <td><strong>{{.Login}}</strong></td>
  <td>{{.FullName}}</td>
  <td>{{if .IsAdmin}}✓{{end}}</td>
  <td style="font-size:12px;color:#94a3b8">{{.CreatedAt.Format "02.01.2006"}}</td>
  <td style="display:flex;gap:6px">
    <a class="btn btn-sm btn-secondary" href="/ui/admin/users/{{.ID}}/roles">Роли</a>
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

func (s *Server) adminSessions(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if s.authRepo == nil {
		adminTmpl.ExecuteTemplate(w, "admin-sessions", map[string]any{"Sessions": nil, "NoAuth": true})
		return
	}
	hasUsers, _ := s.authRepo.HasUsers(r.Context())
	if !hasUsers {
		adminTmpl.ExecuteTemplate(w, "admin-sessions", map[string]any{"Sessions": nil, "NoAuth": true})
		return
	}
	sessions, _ := s.authRepo.ActiveSessions(r.Context())
	adminTmpl.ExecuteTemplate(w, "admin-sessions", map[string]any{"Sessions": sessions})
}

func (s *Server) adminKickUser(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	login := chi.URLParam(r, "login")
	if s.authRepo != nil {
		s.authRepo.KickUser(r.Context(), login)
	}
	http.Redirect(w, r, "/ui/admin/sessions", http.StatusFound)
}

func (s *Server) adminCleanup(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	registers := s.reg.Registers()
	entities := s.reg.Entities()

	if r.Method == http.MethodPost {
		deleted := s.store.DeleteOrphanMovements(r.Context(), registers, entities)
		http.Redirect(w, r, fmt.Sprintf("/ui/admin/cleanup?deleted=%d", deleted), http.StatusFound)
		return
	}

	stats := s.store.OrphanMovements(r.Context(), registers, entities)
	deletedStr := r.URL.Query().Get("deleted")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	adminTmpl.ExecuteTemplate(w, "admin-cleanup", map[string]any{
		"Stats":   stats,
		"Deleted": deletedStr,
	})
}

func (s *Server) adminRoles(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if s.authRepo == nil {
		http.Error(w, "auth not configured", 500)
		return
	}
	roles, err := s.authRepo.ListRoles(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	adminTmpl.ExecuteTemplate(w, "admin-roles", map[string]any{"Roles": roles})
}

func (s *Server) adminUserRoles(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	userID := chi.URLParam(r, "id")
	users, _ := s.authRepo.List(r.Context())
	var userLogin string
	for _, u := range users {
		if u.ID == userID {
			userLogin = u.Login
			break
		}
	}
	allRoles, _ := s.authRepo.ListRoles(r.Context())
	userRoleIDs, _ := s.authRepo.GetUserRoleIDs(r.Context(), userID)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	adminTmpl.ExecuteTemplate(w, "admin-user-roles", map[string]any{
		"UserID":     userID,
		"UserLogin":  userLogin,
		"AllRoles":   allRoles,
		"UserRoleIDs": userRoleIDs,
	})
}

func (s *Server) adminUserRolesUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	userID := chi.URLParam(r, "id")
	r.ParseForm()
	selectedRoleIDs := r.Form["role_id"]
	selectedSet := make(map[string]bool, len(selectedRoleIDs))
	for _, id := range selectedRoleIDs {
		selectedSet[id] = true
	}

	allRoles, _ := s.authRepo.ListRoles(r.Context())
	currentIDs, _ := s.authRepo.GetUserRoleIDs(r.Context(), userID)

	for _, role := range allRoles {
		if selectedSet[role.ID] && !currentIDs[role.ID] {
			s.authRepo.AssignRole(r.Context(), userID, role.ID)
		} else if !selectedSet[role.ID] && currentIDs[role.ID] {
			s.authRepo.UnassignRole(r.Context(), userID, role.ID)
		}
	}
	http.Redirect(w, r, "/ui/admin/users", http.StatusFound)
}

type auditFilterView struct {
	UserLogin   string
	Action      string
	EntityName  string
	DateFromStr string
	DateToStr   string
}

func (s *Server) adminAudit(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	const pageSize = 50
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}

	fv := auditFilterView{
		UserLogin:   q.Get("user"),
		Action:      q.Get("action"),
		EntityName:  q.Get("entity"),
		DateFromStr: q.Get("date_from"),
		DateToStr:   q.Get("date_to"),
	}
	filter := storage.AuditFilter{
		UserLogin:  fv.UserLogin,
		Action:     fv.Action,
		EntityName: fv.EntityName,
	}
	if fv.DateFromStr != "" {
		if t, err := time.Parse("2006-01-02", fv.DateFromStr); err == nil {
			filter.DateFrom = &t
		}
	}
	if fv.DateToStr != "" {
		if t, err := time.Parse("2006-01-02", fv.DateToStr); err == nil {
			t2 := t.Add(24*time.Hour - time.Second)
			filter.DateTo = &t2
		}
	}

	entries, _ := s.store.AuditSearch(r.Context(), filter, pageSize+1, (page-1)*pageSize)

	hasNext := len(entries) > pageSize
	if hasNext {
		entries = entries[:pageSize]
	}

	buildQuery := func(p int) string {
		vals := url.Values{}
		if fv.UserLogin != "" {
			vals.Set("user", fv.UserLogin)
		}
		if fv.Action != "" {
			vals.Set("action", fv.Action)
		}
		if fv.EntityName != "" {
			vals.Set("entity", fv.EntityName)
		}
		if fv.DateFromStr != "" {
			vals.Set("date_from", fv.DateFromStr)
		}
		if fv.DateToStr != "" {
			vals.Set("date_to", fv.DateToStr)
		}
		vals.Set("page", strconv.Itoa(p))
		return vals.Encode()
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	adminTmpl.ExecuteTemplate(w, "admin-audit", map[string]any{
		"Filter":    fv,
		"Entries":   entries,
		"Page":      page,
		"HasPrev":   page > 1,
		"HasNext":   hasNext,
		"PrevQuery": buildQuery(page - 1),
		"NextQuery": buildQuery(page + 1),
	})
}

func (s *Server) recordHistory(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	entries, err := s.store.AuditByRecord(r.Context(), entity.Name, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "page-history", map[string]any{
		"Nav":        s.buildNav(),
		"EntityName": entity.Name,
		"ID":         id.String(),
		"Entries":    entries,
		"BackURL":    fmt.Sprintf("/ui/%s/%s/%s", strings.ToLower(string(entity.Kind)), strings.ToLower(entity.Name), id.String()),
	})
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

const tplAdminSessions = `{{define "admin-sessions"}}` + adminHead + `
<main>
<div class="row-top" style="max-width:700px">
  <h2>Активные пользователи</h2>
  <a class="btn" href="/ui/admin/sessions" style="background:#e2e8f0;color:#475569;font-size:13px">Обновить</a>
</div>
{{if .NoAuth}}
<div class="card" style="max-width:700px">
  <p class="empty">Авторизация не настроена — пользователей нет.</p>
</div>
{{else if .Sessions}}
<div class="card" style="max-width:700px">
<table>
<thead><tr>
  <th>Логин</th><th>Имя</th><th>Роль</th><th>Сессия до</th><th style="width:100px"></th>
</tr></thead>
<tbody>
{{range .Sessions}}<tr>
  <td><strong>{{.Login}}</strong></td>
  <td>{{.FullName}}</td>
  <td>{{if .IsAdmin}}<span style="color:#3b82f6">Администратор</span>{{else}}Пользователь{{end}}</td>
  <td style="font-size:12px;color:#94a3b8">{{.ExpiresAt.Format "02.01.2006 15:04"}}</td>
  <td>
    <form method="POST" action="/ui/admin/sessions/{{.Login}}/kick"
          onsubmit="return confirm('Принудительно завершить все сессии {{.Login}}?')">
      <button class="btn btn-sm btn-danger" type="submit">Выгнать</button>
    </form>
  </td>
</tr>{{end}}
</tbody>
</table>
</div>
{{else}}
<div class="card" style="max-width:700px">
  <p class="empty">Активных сессий нет.</p>
</div>
{{end}}
</main></body></html>
{{end}}`

const tplAdminRoles = `{{define "admin-roles"}}` + adminHead + `
<main>
<h2>Роли и права доступа</h2>
<p style="color:#64748b;font-size:13px;margin-bottom:16px">Роли загружаются из файлов <code>roles/*.yaml</code> в директории проекта и синхронизируются при старте.</p>
{{if .Roles}}
<div class="card" style="max-width:800px">
<table>
<thead><tr><th>Роль</th><th>Описание</th><th>Справочники</th><th>Документы</th><th>Отчёты</th></tr></thead>
<tbody>
{{range .Roles}}<tr>
  <td><strong>{{.Name}}</strong></td>
  <td style="color:#64748b">{{.Description}}</td>
  <td style="font-size:12px">{{range $k,$v := .Permissions.Catalogs}}{{$k}}: {{range $i,$op := $v}}{{if $i}}, {{end}}{{$op}}{{end}}<br>{{end}}</td>
  <td style="font-size:12px">{{range $k,$v := .Permissions.Documents}}{{$k}}: {{range $i,$op := $v}}{{if $i}}, {{end}}{{$op}}{{end}}<br>{{end}}</td>
  <td style="font-size:12px">{{range $k,$v := .Permissions.Reports}}{{$k}}: {{range $i,$op := $v}}{{if $i}}, {{end}}{{$op}}{{end}}<br>{{end}}</td>
</tr>{{end}}
</tbody>
</table>
</div>
{{else}}
<div class="card" style="max-width:600px">
  <p class="empty">Роли не найдены. Создайте файлы <code>roles/*.yaml</code> в директории проекта.</p>
</div>
{{end}}
</main></body></html>
{{end}}`

const tplAdminUserRoles = `{{define "admin-user-roles"}}` + adminHead + `
<main>
<div class="row-top" style="max-width:600px">
  <h2>Роли пользователя: {{.UserLogin}}</h2>
  <a class="btn" href="/ui/admin/users" style="background:#e2e8f0;color:#475569">← Назад</a>
</div>
<div class="card" style="max-width:600px">
<form method="POST">
{{if .AllRoles}}
<table style="margin-bottom:16px">
<thead><tr><th style="width:40px"></th><th>Роль</th><th>Описание</th></tr></thead>
<tbody>
{{range .AllRoles}}<tr>
  <td><input type="checkbox" name="role_id" value="{{.ID}}" {{if index $.UserRoleIDs .ID}}checked{{end}}></td>
  <td><strong>{{.Name}}</strong></td>
  <td style="color:#64748b;font-size:13px">{{.Description}}</td>
</tr>{{end}}
</tbody>
</table>
{{else}}
<p class="empty" style="margin-bottom:16px">Роли не найдены. Создайте roles/*.yaml в директории проекта.</p>
{{end}}
<button class="btn btn-primary" type="submit">Сохранить</button>
</form>
</div>
</main></body></html>
{{end}}`

const tplAdminAudit = `{{define "admin-audit"}}` + adminHead + `
<main>
<div class="row-top" style="max-width:1100px">
  <h2>Журнал изменений</h2>
</div>
<form method="GET" action="" style="max-width:1100px;background:#fff;border-radius:10px;padding:16px 20px;box-shadow:0 1px 3px rgba(0,0,0,.1);margin-bottom:16px;display:flex;gap:12px;flex-wrap:wrap;align-items:flex-end">
  <div>
    <label style="display:block;font-size:12px;color:#64748b;margin-bottom:4px">Пользователь</label>
    <input type="text" name="user" value="{{.Filter.UserLogin}}" placeholder="логин" style="padding:7px 10px;font-size:13px;border:1px solid #e2e8f0;border-radius:7px;width:140px">
  </div>
  <div>
    <label style="display:block;font-size:12px;color:#64748b;margin-bottom:4px">Действие</label>
    <select name="action" style="padding:7px 10px;font-size:13px;border:1px solid #e2e8f0;border-radius:7px">
      <option value="">— все —</option>
      <option value="create" {{if eq .Filter.Action "create"}}selected{{end}}>create</option>
      <option value="update" {{if eq .Filter.Action "update"}}selected{{end}}>update</option>
      <option value="delete" {{if eq .Filter.Action "delete"}}selected{{end}}>delete</option>
      <option value="post"   {{if eq .Filter.Action "post"}}selected{{end}}>post</option>
      <option value="unpost" {{if eq .Filter.Action "unpost"}}selected{{end}}>unpost</option>
      <option value="login"  {{if eq .Filter.Action "login"}}selected{{end}}>login</option>
      <option value="logout" {{if eq .Filter.Action "logout"}}selected{{end}}>logout</option>
    </select>
  </div>
  <div>
    <label style="display:block;font-size:12px;color:#64748b;margin-bottom:4px">Сущность</label>
    <input type="text" name="entity" value="{{.Filter.EntityName}}" placeholder="имя" style="padding:7px 10px;font-size:13px;border:1px solid #e2e8f0;border-radius:7px;width:140px">
  </div>
  <div>
    <label style="display:block;font-size:12px;color:#64748b;margin-bottom:4px">С даты</label>
    <input type="date" name="date_from" value="{{.Filter.DateFromStr}}" style="padding:7px 10px;font-size:13px;border:1px solid #e2e8f0;border-radius:7px">
  </div>
  <div>
    <label style="display:block;font-size:12px;color:#64748b;margin-bottom:4px">По дату</label>
    <input type="date" name="date_to" value="{{.Filter.DateToStr}}" style="padding:7px 10px;font-size:13px;border:1px solid #e2e8f0;border-radius:7px">
  </div>
  <button class="btn btn-primary btn-sm" type="submit">Найти</button>
  <a class="btn btn-sm" href="/ui/admin/audit" style="background:#e2e8f0;color:#475569">Сбросить</a>
</form>

<div class="card" style="max-width:1100px">
{{if .Entries}}
<table style="font-size:13px">
<thead><tr>
  <th>Время</th><th>Пользователь</th><th>Действие</th><th>Сущность</th><th>Поле</th><th>Старое</th><th>Новое</th>
</tr></thead>
<tbody>
{{range .Entries}}<tr>
  <td style="white-space:nowrap;color:#94a3b8">{{.At.Format "02.01.2006 15:04:05"}}</td>
  <td>{{.UserLogin}}</td>
  <td><span style="font-family:monospace;font-size:11px;background:#f1f5f9;padding:2px 6px;border-radius:4px">{{.Action}}</span></td>
  <td style="font-size:12px">{{if .EntityName}}<strong>{{.EntityName}}</strong>{{if .RecordID}}<br><span style="color:#94a3b8">{{.RecordID}}</span>{{end}}{{end}}</td>
  <td style="font-family:monospace;font-size:11px">{{.Field}}</td>
  <td style="font-size:12px;color:#dc2626;max-width:150px;word-break:break-all">{{.OldValue}}</td>
  <td style="font-size:12px;color:#16a34a;max-width:150px;word-break:break-all">{{.NewValue}}</td>
</tr>{{end}}
</tbody>
</table>
<div style="padding:12px 0;display:flex;gap:8px;align-items:center">
  {{if .HasPrev}}<a class="btn btn-sm" href="?{{.PrevQuery}}" style="background:#e2e8f0;color:#475569">← Пред.</a>{{end}}
  <span style="font-size:13px;color:#64748b">Стр. {{.Page}}</span>
  {{if .HasNext}}<a class="btn btn-sm" href="?{{.NextQuery}}" style="background:#e2e8f0;color:#475569">След. →</a>{{end}}
</div>
{{else}}
<p class="empty">Записей не найдено.</p>
{{end}}
</div>
</main></body></html>
{{end}}`

const tplAdminCleanup = `{{define "admin-cleanup"}}` + adminHead + `
<main>
<h2>Очистка регистров</h2>
<p style="color:#64748b;font-size:14px;margin-bottom:20px">
  Осиротевшие движения — строки в регистрах, документ которых уже удалён.
</p>
{{if .Deleted}}
<div style="background:#f0fdf4;border:1px solid #bbf7d0;color:#16a34a;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:14px">
  Удалено строк: {{.Deleted}}
</div>
{{end}}
{{if .Stats}}
<div class="card" style="max-width:700px;margin-bottom:20px">
<table>
<thead><tr>
  <th>Регистр</th><th>Вид регистратора</th><th style="text-align:right">Строк</th>
</tr></thead>
<tbody>
{{range .Stats}}<tr>
  <td>{{.RegisterName}}</td>
  <td>{{.RecorderType}}</td>
  <td style="text-align:right;color:#ef4444;font-weight:600">{{.Count}}</td>
</tr>{{end}}
</tbody>
</table>
</div>
<form method="POST" action="/ui/admin/cleanup"
      onsubmit="return confirm('Удалить все осиротевшие движения?')">
  <button class="btn btn-danger" type="submit">Удалить осиротевшие движения</button>
  <a class="btn" href="/ui" style="background:#e2e8f0;color:#475569;margin-left:8px">Отмена</a>
</form>
{{else}}
<div class="card" style="max-width:600px">
  <p class="empty">Осиротевших движений не найдено — регистры чисты.</p>
</div>
{{end}}
</main></body></html>
{{end}}`
