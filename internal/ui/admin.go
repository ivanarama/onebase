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
	"github.com/ivantit66/onebase/internal/dbcheck"
	"github.com/ivantit66/onebase/internal/storage"
)

var adminTmpl = template.Must(template.New("admin").Parse(tplAdminUsers + tplAdminUserCard + tplAdminUserForm + tplAdminPasswd + tplAdminSessions + tplAdminAPITokens + tplAdminCleanup + tplAdminRoles + tplAdminUserRoles + tplAdminAudit + tplAdminWebhooks + tplAdminRLS + tplAdminAuth + tplAdminAuthProvider + tplProfile2FA))

const tplAdminUsers = `{{define "admin-users"}}` + adminHead + `
<main>
<div class="row-top" style="max-width:800px">
  <h2>Пользователи</h2>
  <a class="btn btn-primary" href="/ui/admin/users/new">+ Добавить</a>
</div>
<div class="card" style="max-width:800px">
{{if .Users}}
<table>
<thead><tr>
  <th>Логин</th><th>Имя</th><th title="Администратор">Админ</th><th title="Показывать в списках выбора">В списке</th><th title="Запрет смены пароля">Пароль</th><th>Создан</th><th></th>
</tr></thead>
<tbody>
{{range .Users}}<tr>
  <td><a href="/ui/admin/users/{{.ID}}" style="color:#1d4ed8;font-weight:600;text-decoration:none">{{.Login}}</a></td>
  <td style="color:#475569">{{.FullName}}</td>
  <td style="text-align:center">{{if .IsAdmin}}<span style="color:#16a34a;font-weight:700">✓</span>{{end}}</td>
  <td style="text-align:center">{{if .ShowInList}}<span style="color:#2563eb;font-weight:700">✓</span>{{else}}<span style="color:#cbd5e1">—</span>{{end}}</td>
  <td style="text-align:center">{{if .DenyPasswdChange}}🔒{{else}}<span style="color:#cbd5e1">—</span>{{end}}</td>
  <td style="font-size:12px;color:#94a3b8">{{.CreatedAt.Format "02.01.2006"}}</td>
  <td>
    <div style="display:flex;gap:4px">
      <a class="btn btn-sm btn-secondary" href="/ui/admin/users/{{.ID}}">Карточка</a>
      <a class="btn btn-sm btn-secondary" href="/ui/admin/users/{{.ID}}/roles">Роли</a>
      <form method="POST" action="/ui/admin/users/{{.ID}}/delete" data-ob-confirm="Удалить пользователя {{.Login}}?" style="margin:0">
        <button class="btn btn-sm btn-danger" type="submit">Удалить</button>
      </form>
    </div>
  </td>
</tr>{{end}}
</tbody>
</table>
{{else}}
<p class="empty">Пользователей нет — вход в систему без пароля.<br>Первый пользователь должен быть администратором: после его создания вход потребуется всем.</p>
{{end}}
</div>
</main></body></html>
{{end}}`

const tplAdminUserCard = `{{define "admin-user-card"}}` + adminHead + `
<main>
<div style="margin-bottom:16px"><a href="/ui/admin/users" style="color:#64748b;font-size:13px;text-decoration:none">← Пользователи</a></div>
<h2>{{.User.Login}}</h2>
{{if .Success}}<div style="background:#f0fdf4;border:1px solid #86efac;color:#15803d;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:14px;max-width:560px">✓ {{.Success}}</div>{{end}}
{{if .Error}}<div class="error" style="max-width:560px">{{.Error}}</div>{{end}}

<div class="card" style="max-width:560px;margin-bottom:16px">
<h3 style="margin-bottom:16px">Основные данные</h3>
<form method="POST">
  <input type="hidden" name="action" value="update">
  <div class="form-group">
    <label>Логин</label>
    <input type="text" value="{{.User.Login}}" readonly style="background:#f8fafc;color:#64748b;cursor:default">
  </div>
  <div class="form-group">
    <label>Полное имя</label>
    <input type="text" name="full_name" value="{{.User.FullName}}">
  </div>
  <div class="form-group">
    <label style="display:flex;align-items:center;gap:8px;cursor:pointer;font-weight:400">
      <input type="checkbox" name="is_admin" value="1" {{if .User.IsAdmin}}checked{{end}}> Администратор
    </label>
  </div>
  <div class="form-group">
    <label style="display:flex;align-items:center;gap:8px;cursor:pointer;font-weight:400">
      <input type="checkbox" name="deny_passwd_change" value="1" {{if .User.DenyPasswdChange}}checked{{end}}> Запретить смену пароля пользователем
    </label>
  </div>
  <div class="form-group">
    <label style="display:flex;align-items:center;gap:8px;cursor:pointer;font-weight:400">
      <input type="checkbox" name="show_in_list" value="1" {{if .User.ShowInList}}checked{{end}}> Показывать в списках выбора
    </label>
    <div style="font-size:12px;color:#94a3b8;margin-top:4px;margin-left:24px">Пользователь будет доступен для выбора в полях типа «Ответственный» и т.п.</div>
  </div>
  <div class="form-group">
    <label style="display:flex;align-items:center;gap:8px;cursor:pointer;font-weight:400">
      <input type="checkbox" name="ai_data_access" value="1" {{if .User.AIDataAccess}}checked{{end}}> Доступ ИИ-чата к данным (без прав администратора)
    </label>
    <div style="font-size:12px;color:#dc2626;margin-top:4px;margin-left:24px">⚠ Действует только если в настройках базы (конфигуратор → ИИ-помощник → «Доступ ИИ-чата к данным») выбран режим <b>rbac</b> или <b>all</b>. В <b>rbac</b> запросы ассистента фильтруются по правам чтения пользователя; в <b>all</b> — доступ ко всем данным без проверки прав. По умолчанию (<b>admin_only</b>) флаг не действует. Результаты запросов передаются внешнему LLM-провайдеру; обращения пишутся в журнал ИИ (_ai_audit).</div>
  </div>
  <button class="btn btn-primary" type="submit">Сохранить</button>
</form>
</div>

<div class="card" style="max-width:560px;margin-bottom:16px">
<h3 style="margin-bottom:16px">Второй фактор</h3>
{{if .TwoFactor}}
  {{if .TwoFactor.Enabled}}
  <p style="font-size:14px;color:#15803d;font-weight:600;margin-bottom:8px">✓ Включён</p>
  <p style="font-size:13px;color:#475569;margin-bottom:12px">Осталось резервных кодов: <b>{{.TwoFactor.BackupCodesLeft}}</b>. Сброс нужен, когда пользователь потерял аутентификатор и резервные коды: после сброса он настроит второй фактор заново при следующем входе.</p>
  <form method="POST" data-ob-confirm="Сбросить второй фактор пользователя {{.User.Login}}?">
    <input type="hidden" name="action" value="reset_2fa">
    <button class="btn btn-danger" type="submit">Сбросить второй фактор</button>
  </form>
  {{else}}
  <p style="font-size:14px;color:#64748b;margin-bottom:12px">Выключен.</p>
  <p style="font-size:13px;color:#475569;margin-bottom:12px">Если политика базы требует второй фактор, пользователь привязывает его при входе. Пока самопривязка по паролю выключена (значение по умолчанию), для привязки нужен одноразовый <b>код привязки</b>: выдайте его и передайте пользователю — он введёт код после пароля. Без кода привязать фактор по одному паролю нельзя. Код действует {{.BindTicketTTLHours}} ч; новый заменяет прежний.</p>
  {{if .BindTicket}}
  <div class="secret" style="font-family:Consolas,monospace;font-size:15px;letter-spacing:1px;background:#f0fdf4;border:1px solid #86efac;border-radius:5px;padding:10px;text-align:center;margin-bottom:8px;word-break:break-all">{{.BindTicket}}</div>
  <p style="font-size:12px;color:#b45309;margin-bottom:12px">Покажите код пользователю сейчас — второй раз он не отобразится.</p>
  {{end}}
  <form method="POST">
    <input type="hidden" name="action" value="issue_bind_ticket">
    <button class="btn btn-primary" type="submit">Выдать код привязки</button>
  </form>
  {{end}}
{{else}}
  <p style="font-size:13px;color:#94a3b8">Состояние недоступно.</p>
{{end}}
</div>

<div class="card" style="max-width:560px">
<h3 style="margin-bottom:16px">Изменить пароль</h3>
<form method="POST">
  <input type="hidden" name="action" value="passwd">
  <div class="form-group">
    <label>Новый пароль</label>
    <input type="password" name="new_password" autocomplete="new-password" minlength="{{.MinPasswordLength}}" {{if .PasswordRequired}}required{{end}}>
  </div>
  <div class="form-group">
    <label>Повторите пароль</label>
    <input type="password" name="confirm_password" autocomplete="new-password" minlength="{{.MinPasswordLength}}" {{if .PasswordRequired}}required{{end}}>
  </div>
  <button class="btn" type="submit" style="background:#f59e0b;color:#fff">Изменить пароль</button>
</form>
</div>
</main></body></html>
{{end}}`

const tplAdminUserForm = `{{define "admin-user-form"}}` + adminHead + `
<main>
<h2>Добавить пользователя</h2>
{{if .Error}}<div class="error" style="max-width:500px">{{.Error}}</div>{{end}}
{{if .FirstUser}}<div style="background:#fff7ed;border:1px solid #fdba74;color:#9a3412;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:14px;max-width:500px">После создания первого пользователя вход в систему потребуется всем, включая вас. Первый пользователь должен быть администратором — иначе управлять учётными записями будет некому.</div>{{end}}
<div class="card" style="max-width:500px">
<form method="POST">
  <div class="form-group">
    <label>Логин</label>
    <input type="text" name="login" value="{{.Login}}" required autofocus>
  </div>
  <div class="form-group">
    <label>Полное имя</label>
    <input type="text" name="full_name" value="{{.FullName}}">
  </div>
  <div class="form-group">
    <label>Пароль</label>
    <input type="password" name="password" minlength="{{.MinPasswordLength}}" {{if .PasswordRequired}}required{{end}}>
  </div>
  <div class="form-group">
    <label style="display:flex;align-items:center;gap:8px;cursor:pointer">
      <input type="checkbox" name="is_admin" value="1"{{if .AdminChecked}} checked{{end}}> Администратор
    </label>
  </div>
  <div class="form-group">
    <label style="display:flex;align-items:center;gap:8px;cursor:pointer">
      <input type="checkbox" name="deny_passwd_change" value="1"> Запретить смену пароля
    </label>
  </div>
  <div class="form-group">
    <label style="display:flex;align-items:center;gap:8px;cursor:pointer">
      <input type="checkbox" name="show_in_list" value="1"> Показывать в списках выбора
    </label>
  </div>
  <div class="form-group">
    <label style="display:flex;align-items:center;gap:8px;cursor:pointer">
      <input type="checkbox" name="ai_data_access" value="1"> Доступ ИИ-чата к данным (без прав администратора)
    </label>
    <div style="font-size:12px;color:#dc2626;margin-top:4px;margin-left:24px">⚠ Действует только в режиме rbac/all (конфигуратор → ИИ-помощник). В rbac запросы фильтруются по правам чтения пользователя; результаты уходят внешнему LLM-провайдеру.</div>
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
<script src="/static/ui.js"></script>
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
input[type=text],input[type=password],input[type=date],select{width:100%;padding:9px 12px;border:1px solid #e2e8f0;border-radius:7px;font-size:14px;background:#fff}
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
		s.renderForbidden(w, r)
		return
	}
	users, err := s.authRepo.List(r.Context())
	if err != nil {
		http.Error(w, s.errText(r, err), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderAdminTemplate(w, "admin-users", map[string]any{"Users": users})
}

func (s *Server) adminUserCard(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	lang := s.resolveLang(r)
	userID := chi.URLParam(r, "id")
	u, err := s.authRepo.GetByID(r.Context(), userID)
	if err != nil {
		http.Error(w, s.tr(lang, "Пользователь не найден"), 404)
		return
	}
	data := map[string]any{"User": u}
	s.addPasswordPolicyData(data)
	data["BindTicketTTLHours"] = int(auth.BindTicketTTL / time.Hour)
	if info, infoErr := s.authRepo.TwoFactorInfoFor(r.Context(), userID); infoErr == nil {
		data["TwoFactor"] = info
	}

	if r.Method == http.MethodPost {
		// Сбой разбора формы игнорировать нельзя: дальше все FormValue вернут
		// пустые строки, и обработчик воспримет это как осознанный ввод —
		// сотрёт полное имя и снимет флаги (is_admin, show_in_list). То есть
		// битый запрос молча понижал бы права пользователя. Отказываем явно.
		//
		// Разбор ограниченный (limitMultipartRequest + parseBoundedForm) — тот
		// же приём, что на пути событий управляемых форм. Голый r.ParseForm()
		// читает тело целиком без предела: gosec помечает это как G120.
		s.limitMultipartRequest(w, r)
		if err := parseBoundedForm(r, defaultFormMemoryBytes); err != nil {
			http.Error(w, s.errText(r, err), uploadErrorStatus(err))
			return
		}
		switch r.FormValue("action") {
		case "update":
			fullName := r.FormValue("full_name")
			isAdmin := r.FormValue("is_admin") == "1"
			denyPasswd := r.FormValue("deny_passwd_change") == "1"
			showInList := r.FormValue("show_in_list") == "1"
			aiData := r.FormValue("ai_data_access") == "1"
			if err := s.authRepo.Update(r.Context(), userID, fullName, isAdmin, denyPasswd, showInList, aiData); err != nil {
				data["Error"] = s.errText(r, err)
			} else {
				u.FullName = fullName
				u.IsAdmin = isAdmin
				u.DenyPasswdChange = denyPasswd
				u.ShowInList = showInList
				u.AIDataAccess = aiData
				data["Success"] = s.tr(lang, "Данные сохранены")
			}
		case "reset_2fa":
			// Административный сброс второго фактора. Без него потеря
			// аутентификатора при израсходованных резервных кодах означала
			// потерю доступа, а при require_2fa_admins и единственном
			// администраторе — неадминистрируемую базу: в конфигуратор такой
			// администратор тоже не попадёт, его отправят включать 2FA в
			// Предприятие, куда он войти уже не может.
			if err := s.authRepo.DisableTOTP(r.Context(), userID); err != nil {
				data["Error"] = s.errText(r, err)
			} else {
				s.logSessionAudit(r, "2fa_reset_by_admin", u.Login, userID)
				if info, infoErr := s.authRepo.TwoFactorInfoFor(r.Context(), userID); infoErr == nil {
					data["TwoFactor"] = info
				}
				data["Success"] = s.tr(lang, "Второй фактор сброшен — пользователь настроит его заново при следующем входе")
			}
		case "issue_bind_ticket":
			// Одноразовый код привязки второго фактора (issue #577): им
			// пользователь подтверждает на входе право привязать 2FA, когда
			// самопривязка по паролю выключена политикой. Показывается один раз.
			code, terr := s.authRepo.IssueBindTicket(r.Context(), userID)
			if terr != nil {
				data["Error"] = s.errText(r, terr)
			} else {
				s.logSessionAudit(r, "2fa_bind_ticket_issued", u.Login, userID)
				data["BindTicket"] = code
				data["Success"] = s.tr(lang, "Код привязки выдан — передайте его пользователю")
			}
		case "passwd":
			newPwd := r.FormValue("new_password")
			confirm := r.FormValue("confirm_password")
			if newPwd != confirm {
				data["Error"] = s.tr(lang, "Пароли не совпадают")
			} else if err := s.authRepo.UpdatePassword(r.Context(), userID, newPwd); err != nil {
				data["Error"] = s.errText(r, err)
			} else if rerr := s.revokeSessionsOnPasswordChange(r, userID, u.Login); rerr != nil {
				data["Error"] = s.tr(lang, "Пароль изменён, но сессии пользователя не завершены") + ": " + s.errText(r, rerr)
			} else {
				data["Success"] = s.tr(lang, "Пароль изменён")
			}
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderAdminTemplate(w, "admin-user-card", data)
}

func (s *Server) adminUserNew(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderAdminTemplate(w, "admin-user-form", s.adminUserFormData(r, "", "", "", false))
}

func (s *Server) adminUserFormData(r *http.Request, errMsg, login, fullName string, adminChecked bool) map[string]any {
	firstUser := false
	if s.authRepo != nil {
		if hasUsers, err := s.authRepo.HasUsers(r.Context()); err == nil && !hasUsers {
			firstUser = true
			adminChecked = true
		}
	}
	data := map[string]any{
		"Error":        errMsg,
		"FirstUser":    firstUser,
		"AdminChecked": adminChecked,
		"Login":        login,
		"FullName":     fullName,
	}
	s.addPasswordPolicyData(data)
	return data
}

func (s *Server) adminUserCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	lang := s.resolveLang(r)
	s.limitMultipartRequest(w, r)
	if err := parseBoundedForm(r, defaultFormMemoryBytes); err != nil {
		http.Error(w, s.errText(r, err), uploadErrorStatus(err))
		return
	}
	login := r.FormValue("login")
	password := r.FormValue("password")
	fullName := r.FormValue("full_name")
	isAdmin := r.FormValue("is_admin") == "1"
	denyPasswd := r.FormValue("deny_passwd_change") == "1"
	showInList := r.FormValue("show_in_list") == "1"
	aiData := r.FormValue("ai_data_access") == "1"

	if login == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := s.adminUserFormData(r, s.tr(lang, "Логин обязателен"), login, fullName, isAdmin)
		renderAdminTemplate(w, "admin-user-form", data)
		return
	}

	u, err := s.authRepo.CreateManaged(r.Context(), login, password, fullName, isAdmin)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := s.adminUserFormData(r, s.errText(r, err), login, fullName, isAdmin)
		renderAdminTemplate(w, "admin-user-form", data)
		return
	}
	if denyPasswd || showInList || aiData {
		// Ошибку применения флагов (в т.ч. ai_data_access — доступ ИИ к данным)
		// не глотаем: иначе админ уверен, что выставил флаг, а он не применился.
		if err := s.authRepo.Update(r.Context(), u.ID, fullName, isAdmin, denyPasswd, showInList, aiData); err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			data := s.adminUserFormData(r, s.errText(r, err), login, fullName, isAdmin)
			renderAdminTemplate(w, "admin-user-form", data)
			return
		}
	}
	http.Redirect(w, r, "/ui/admin/users", http.StatusFound)
}

func (s *Server) adminUserDenyPasswd(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	userID := chi.URLParam(r, "id")
	users, _ := s.authRepo.List(r.Context())
	var current bool
	for _, u := range users {
		if u.ID == userID {
			current = u.DenyPasswdChange
			break
		}
	}
	// Флаг управляет тем, может ли пользователь сменить себе пароль. Молча не
	// переключившийся запрет админ увидит только по неизменившейся галочке.
	if err := s.authRepo.SetDenyPasswdChange(r.Context(), userID, !current); err != nil {
		http.Error(w, s.errText(r, err), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/ui/admin/users", http.StatusFound)
}

func (s *Server) adminUserPasswd(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	lang := s.resolveLang(r)
	userID := chi.URLParam(r, "id")
	users, _ := s.authRepo.List(r.Context())
	var userLogin string
	for _, u := range users {
		if u.ID == userID {
			userLogin = u.Login
			break
		}
	}
	data := map[string]any{
		"UserLogin": userLogin,
		"BackURL":   "/ui/admin/users",
		"NeedOld":   false,
	}
	s.addPasswordPolicyData(data)
	if r.Method == http.MethodPost {
		s.limitMultipartRequest(w, r)
		if err := parseBoundedForm(r, defaultFormMemoryBytes); err != nil {
			http.Error(w, s.errText(r, err), uploadErrorStatus(err))
			return
		}
		newPwd := r.FormValue("new_password")
		confirm := r.FormValue("confirm_password")
		if newPwd != confirm {
			data["Error"] = s.tr(lang, "Пароли не совпадают")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			renderAdminTemplate(w, "admin-passwd", data)
			return
		}
		if err := s.authRepo.UpdatePassword(r.Context(), userID, newPwd); err != nil {
			data["Error"] = s.errText(r, err)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			renderAdminTemplate(w, "admin-passwd", data)
			return
		}
		if rerr := s.revokeSessionsOnPasswordChange(r, userID, userLogin); rerr != nil {
			data["Error"] = s.tr(lang, "Пароль изменён, но сессии пользователя не завершены") + ": " + s.errText(r, rerr)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			renderAdminTemplate(w, "admin-passwd", data)
			return
		}
		data["Success"] = true
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderAdminTemplate(w, "admin-passwd", data)
}

// revokeSessionsOnPasswordChange — политика плана 78: смена пароля админом
// завершает все сессии пользователя; смена собственного пароля — все сессии,
// кроме текущей (текущее окно продолжает работать). Событие пишется в аудит.
// Ошибка отзыва возвращается вызывающему: пароль к этому моменту уже сменён,
// откатить нельзя, но и молчать нельзя — сбой отзыва означает, что украденная
// сессия пережила смену пароля, то есть ровно то, ради чего отзыв и делается.
// Запись в аудит при этом НЕ пишется: «сессии отозваны» в журнале, когда они не
// отозваны, хуже отсутствия записи — по ней инцидент закроют как отработанный.
func (s *Server) revokeSessionsOnPasswordChange(r *http.Request, targetUserID, targetLogin string) error {
	ctx := r.Context()
	actor := auth.UserFromContext(ctx)
	var err error
	if actor != nil && actor.ID == targetUserID {
		if cookie, cerr := r.Cookie("onebase_session"); cerr == nil && cookie.Value != "" {
			err = s.authRepo.KickOtherSessions(ctx, targetUserID, cookie.Value)
		} else {
			err = s.authRepo.KickUserSessions(ctx, targetUserID)
		}
	} else {
		err = s.authRepo.KickUserSessions(ctx, targetUserID)
	}
	if err != nil {
		return err
	}
	s.logSessionAudit(r, "password_change_sessions_revoked", targetLogin, targetUserID)
	return nil
}

// selfPasswd lets any authenticated user change their own password.
func (s *Server) selfPasswd(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	lang := s.resolveLang(r)
	u := auth.UserFromContext(r.Context())
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	if u.DenyPasswdChange {
		s.renderForbidden(w, r)
		return
	}
	data := map[string]any{
		"UserLogin":   u.Login,
		"BackURL":     "/ui",
		"NeedOld":     true,
		"SelfService": true,
		"OthersOut":   r.URL.Query().Get("others_out") == "1",
	}
	s.addPasswordPolicyData(data)
	if r.Method == http.MethodPost {
		s.limitMultipartRequest(w, r)
		if err := parseBoundedForm(r, defaultFormMemoryBytes); err != nil {
			http.Error(w, s.errText(r, err), uploadErrorStatus(err))
			return
		}
		oldPwd := r.FormValue("old_password")
		newPwd := r.FormValue("new_password")
		confirm := r.FormValue("confirm_password")

		if _, err := s.authRepo.Authenticate(r.Context(), u.Login, oldPwd); err != nil {
			data["Error"] = s.tr(lang, "Неверный текущий пароль")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			renderAdminTemplate(w, "admin-passwd", data)
			return
		}
		if newPwd != confirm {
			data["Error"] = s.tr(lang, "Пароли не совпадают")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			renderAdminTemplate(w, "admin-passwd", data)
			return
		}
		if err := s.authRepo.UpdatePassword(r.Context(), u.ID, newPwd); err != nil {
			data["Error"] = s.errText(r, err)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			renderAdminTemplate(w, "admin-passwd", data)
			return
		}
		if rerr := s.revokeSessionsOnPasswordChange(r, u.ID, u.Login); rerr != nil {
			data["Error"] = s.tr(lang, "Пароль изменён, но сессии пользователя не завершены") + ": " + s.errText(r, rerr)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			renderAdminTemplate(w, "admin-passwd", data)
			return
		}
		data["Success"] = true
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderAdminTemplate(w, "admin-passwd", data)
}

func (s *Server) addPasswordPolicyData(data map[string]any) {
	policy := auth.PasswordPolicy{MinLength: auth.DefaultMinPasswordLength}
	if s.authRepo != nil {
		policy = s.authRepo.PasswordPolicy()
	}
	data["MinPasswordLength"] = policy.MinLength
	data["PasswordRequired"] = !policy.AllowEmpty
}

func (s *Server) adminUserDelete(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.authRepo.Delete(r.Context(), id); err != nil {
		http.Error(w, s.errText(r, err), http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/ui/admin/users", http.StatusFound)
}

func (s *Server) adminSessions(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	hasUsers := false
	if s.authRepo != nil {
		hasUsers, _ = s.authRepo.HasUsers(r.Context())
	}
	if !hasUsers {
		users, _ := s.store.ActiveUsersFromAudit(r.Context())
		renderAdminTemplate(w, "admin-sessions", map[string]any{"AuditUsers": users})
		return
	}
	sessions, _ := s.authRepo.ActiveSessions(r.Context())
	limit := 0
	if s.store != nil {
		limit = s.store.GetMaxSessionsPerUser(r.Context())
	}
	renderAdminTemplate(w, "admin-sessions", map[string]any{
		"Sessions":   sessionVMs(sessions),
		"Limit":      limit,
		"LimitSaved": r.URL.Query().Get("limit_saved") == "1",
	})
}

// adminSessionLimit сохраняет политику «максимум сессий на пользователя»
// (план 78, п. 1.6; ключ auth.max_sessions_per_user в _settings).
func (s *Server) adminSessionLimit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	s.limitMultipartRequest(w, r)
	if err := parseBoundedForm(r, defaultFormMemoryBytes); err != nil {
		http.Error(w, s.errText(r, err), uploadErrorStatus(err))
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(r.FormValue("limit")))
	if err != nil || n < 0 {
		n = 0
	}
	if s.store != nil {
		// Редирект ниже прямо утверждает limit_saved=1 — нельзя утверждать это
		// при неудавшейся записи.
		if err := s.store.SaveMaxSessionsPerUser(r.Context(), n); err != nil {
			http.Error(w, s.errText(r, err), http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/ui/admin/sessions?limit_saved=1", http.StatusFound)
}

// sessionVM — строка экрана активных сессий: SessionInfo + отображаемые поля.
type sessionVM struct {
	*auth.SessionInfo
	KindLabel string
	ShortUA   string
}

func sessionVMs(sessions []*auth.SessionInfo) []sessionVM {
	vms := make([]sessionVM, 0, len(sessions))
	for _, si := range sessions {
		vm := sessionVM{SessionInfo: si, ShortUA: shortUserAgent(si.UserAgent)}
		switch si.Kind {
		case auth.SessionKindConfigurator:
			vm.KindLabel = "Конфигуратор"
		case auth.SessionKindEnterprise:
			vm.KindLabel = "Предприятие"
		default:
			vm.KindLabel = "—"
		}
		vms = append(vms, vm)
	}
	return vms
}

// shortUserAgent сжимает user-agent до узнаваемого имени браузера.
func shortUserAgent(ua string) string {
	switch {
	case ua == "":
		return ""
	case strings.Contains(ua, "Edg/"):
		return "Edge"
	case strings.Contains(ua, "OPR/"):
		return "Opera"
	case strings.Contains(ua, "Chrome/"):
		return "Chrome"
	case strings.Contains(ua, "Firefox/"):
		return "Firefox"
	case strings.Contains(ua, "Safari/"):
		return "Safari"
	}
	if r := []rune(ua); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return ua
}

// logSessionAudit пишет событие сессионного аудита (kick/revoke). recordID
// аудита типизирован как UUID, поэтому туда идёт UUID пользователя (или ""),
// а логин цели — в entityName.
func (s *Server) logSessionAudit(r *http.Request, action, targetLogin, targetUserID string) {
	if s.store == nil {
		return
	}
	var actorID, actorLogin string
	if u := auth.UserFromContext(r.Context()); u != nil {
		actorID, actorLogin = u.ID, u.Login
	}
	s.store.LogAction(r.Context(), action, "", targetLogin, targetUserID, actorID, actorLogin, r.RemoteAddr)
}

func (s *Server) adminKickUser(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	login := chi.URLParam(r, "login")
	if s.authRepo != nil {
		if err := s.authRepo.KickUser(r.Context(), login); err == nil {
			s.logSessionAudit(r, "session_kick_all", login, "")
		}
	}
	http.Redirect(w, r, "/ui/admin/sessions", http.StatusFound)
}

// adminKickSession завершает одну сессию по public_id (план 78).
func (s *Server) adminKickSession(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	s.limitMultipartRequest(w, r)
	if err := parseBoundedForm(r, defaultFormMemoryBytes); err != nil {
		http.Error(w, s.errText(r, err), uploadErrorStatus(err))
		return
	}
	publicID := r.FormValue("public_id")
	if publicID != "" && s.authRepo != nil {
		if err := s.authRepo.KickSession(r.Context(), publicID); err == nil {
			s.logSessionAudit(r, "session_kick", r.FormValue("login"), "")
		}
	}
	http.Redirect(w, r, "/ui/admin/sessions", http.StatusFound)
}

// selfLogoutOthers — «выйти со всех устройств, кроме текущего» (план 78).
func (s *Server) selfLogoutOthers(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	if cookie, err := r.Cookie("onebase_session"); err == nil && cookie.Value != "" && s.authRepo != nil {
		if err := s.authRepo.KickOtherSessions(r.Context(), u.ID, cookie.Value); err == nil {
			s.logSessionAudit(r, "session_kick_all", u.Login, u.ID)
		}
	}
	http.Redirect(w, r, "/ui/profile/passwd?others_out=1", http.StatusFound)
}

func (s *Server) adminAPITokens(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	s.renderAdminAPITokens(w, r, nil)
}

func (s *Server) adminAPITokenCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	if s.authRepo == nil {
		http.Error(w, "auth not configured", 500)
		return
	}
	lang := s.resolveLang(r)
	if err := r.ParseForm(); err != nil {
		s.renderAdminAPITokens(w, r, map[string]any{"Error": s.errText(r, err)})
		return
	}
	expiresAt, err := parseAPITokenExpiresAt(r.FormValue("expires_at"))
	if err != nil {
		s.renderAdminAPITokens(w, r, map[string]any{"Error": s.tr(lang, "Некорректная дата истечения")})
		return
	}
	_, raw, err := s.authRepo.CreateAPIToken(r.Context(), r.FormValue("name"), r.FormValue("user_id"), expiresAt)
	if err != nil {
		s.renderAdminAPITokens(w, r, map[string]any{"Error": s.errText(r, err)})
		return
	}
	s.renderAdminAPITokens(w, r, map[string]any{"CreatedToken": raw})
}

func (s *Server) adminAPITokenRevoke(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	if s.authRepo != nil {
		_ = s.authRepo.RevokeAPIToken(r.Context(), chi.URLParam(r, "id"))
	}
	http.Redirect(w, r, "/ui/admin/api-tokens", http.StatusFound)
}

func (s *Server) renderAdminAPITokens(w http.ResponseWriter, r *http.Request, extra map[string]any) {
	if s.authRepo == nil {
		http.Error(w, "auth not configured", 500)
		return
	}
	users, err := s.authRepo.List(r.Context())
	if err != nil {
		http.Error(w, s.errText(r, err), 500)
		return
	}
	tokens, err := s.authRepo.ListAPITokens(r.Context())
	if err != nil {
		http.Error(w, s.errText(r, err), 500)
		return
	}
	data := map[string]any{"Users": users, "Tokens": tokens}
	for k, v := range extra {
		data[k] = v
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderAdminTemplate(w, "admin-api-tokens", data)
}

func parseAPITokenExpiresAt(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	t, err := time.ParseInLocation("2006-01-02", raw, time.Local)
	if err != nil {
		return nil, err
	}
	end := t.Add(24*time.Hour - time.Second)
	return &end, nil
}

// doctorCheckView — результат проверки плюс признак «умеет чиниться»: шаблону
// нужно решить, показывать ли галочку исправления.
type doctorCheckView struct {
	dbcheck.Result
	CanFix bool
}

// adminCleanup — страница диагностики базы: тот же набор проверок, что у
// `onebase doctor` (план 114), в веб-интерфейсе.
//
// Раньше здесь была одна кнопка «удалить все осиротевшие движения» — то есть
// веб был строго слабее CLI и при этом единственным местом, где очистка вообще
// доступна. Теперь оба показывают один и тот же отчёт: расходиться им нечем,
// проверки живут в общем пакете.
func (s *Server) adminCleanup(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	env := &dbcheck.Env{
		DB:            s.store,
		Entities:      s.reg.Entities(),
		Registers:     s.reg.Registers(),
		InfoRegisters: s.reg.InfoRegisters(),
	}

	fix := map[string]bool{}
	fixing := false
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
		if err := parseBoundedForm(r, defaultFormMemoryBytes); err != nil {
			http.Error(w, s.errText(r, err), http.StatusBadRequest)
			return
		}
		for _, name := range r.Form["fix"] {
			if name = strings.TrimSpace(name); name != "" {
				fix[name] = true
				fixing = true
			}
		}
	}

	report := dbcheck.Run(r.Context(), env, dbcheck.All(), fix)
	views := make([]doctorCheckView, 0, len(report.Results))
	fixedTotal := 0
	for _, res := range report.Results {
		views = append(views, doctorCheckView{Result: res, CanFix: dbcheck.CanFix(res.Check)})
		fixedTotal += res.Fixed
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderAdminTemplate(w, "admin-cleanup", map[string]any{
		"Checks":     views,
		"Fixing":     fixing,
		"FixedTotal": fixedTotal,
		"Clean":      report.Worst() == dbcheck.SeverityOK,
	})
}

func (s *Server) adminRoles(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	if s.authRepo == nil {
		http.Error(w, "auth not configured", 500)
		return
	}
	roles, err := s.authRepo.ListRoles(r.Context())
	if err != nil {
		http.Error(w, s.errText(r, err), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderAdminTemplate(w, "admin-roles", map[string]any{"Roles": roles})
}

func (s *Server) adminUserRoles(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
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
	renderAdminTemplate(w, "admin-user-roles", map[string]any{
		"UserID":      userID,
		"UserLogin":   userLogin,
		"AllRoles":    allRoles,
		"UserRoleIDs": userRoleIDs,
	})
}

func (s *Server) adminUserRolesUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	userID := chi.URLParam(r, "id")
	s.limitMultipartRequest(w, r)
	if err := parseBoundedForm(r, defaultFormMemoryBytes); err != nil {
		http.Error(w, s.errText(r, err), uploadErrorStatus(err))
		return
	}
	selectedRoleIDs := r.Form["role_id"]
	selectedSet := make(map[string]bool, len(selectedRoleIDs))
	for _, id := range selectedRoleIDs {
		selectedSet[id] = true
	}

	// Диф считается от текущего состояния, поэтому сбой ЧТЕНИЯ опаснее сбоя
	// записи: при пустом currentIDs ни одна роль не попадёт в ветку снятия,
	// цикл отработает вхолостую, и админ получит редирект на список — то есть
	// подтверждение — на запрос, где он снимал роль.
	allRoles, err := s.authRepo.ListRoles(r.Context())
	if err != nil {
		http.Error(w, s.errText(r, err), http.StatusInternalServerError)
		return
	}
	currentIDs, err := s.authRepo.GetUserRoleIDs(r.Context(), userID)
	if err != nil {
		http.Error(w, s.errText(r, err), http.StatusInternalServerError)
		return
	}

	// Транзакции auth.Repo наружу не даёт: отказ на середине оставляет часть
	// ролей применённой, поэтому в ошибке названа роль, на которой всё встало.
	for _, role := range allRoles {
		var aerr error
		switch {
		case selectedSet[role.ID] && !currentIDs[role.ID]:
			aerr = s.authRepo.AssignRole(r.Context(), userID, role.ID)
		case !selectedSet[role.ID] && currentIDs[role.ID]:
			aerr = s.authRepo.UnassignRole(r.Context(), userID, role.ID)
		}
		if aerr != nil {
			http.Error(w, role.Name+": "+s.errText(r, aerr), http.StatusInternalServerError)
			return
		}
	}
	s.InvalidateWidgetCache()
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
		s.renderForbidden(w, r)
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

	s.enrichAuditEntriesGlobal(r.Context(), entries)
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
	renderAdminTemplate(w, "admin-audit", map[string]any{
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
		http.Error(w, s.errText(r, err), 500)
		return
	}
	s.enrichAuditEntries(r.Context(), entity, entries)
	s.render(w, r, "page-history", map[string]any{
		"EntityName": entity.Name,
		"ID":         id.String(),
		"Entries":    entries,
		"BackURL":    fmt.Sprintf("/ui/%s/%s/%s", strings.ToLower(string(entity.Kind)), strings.ToLower(entity.Name), id.String()),
	})
}

// isAdmin returns true for an authenticated administrator or for the explicit
// first-run mode where auth middleware confirmed that the reachable database
// contains no users. Database errors must never turn into administrator access.
func (s *Server) isAdmin(r *http.Request) bool {
	if s.authRepo == nil {
		return true
	}
	if u := auth.UserFromContext(r.Context()); u != nil {
		return u.IsAdmin
	}
	if auth.OpenAccessFromContext(r.Context()) {
		return true
	}
	// Direct handler tests and a few internal call sites do not pass through the
	// middleware. Preserve bootstrap behavior only after a successful query.
	hasUsers, err := s.authRepo.HasUsers(r.Context())
	return err == nil && !hasUsers
}

const tplAdminPasswd = `{{define "admin-passwd"}}` + adminHead + `
<main>
<div class="row-top" style="max-width:500px">
  <h2>Смена пароля{{if .UserLogin}} — {{.UserLogin}}{{end}}</h2>
  <a class="btn" href="{{.BackURL}}" style="background:#e2e8f0;color:#475569">← Назад</a>
</div>
{{if .Error}}<div class="error" style="max-width:500px">{{.Error}}</div>{{end}}
{{if .Success}}<div style="background:#f0fdf4;border:1px solid #bbf7d0;color:#16a34a;padding:12px;border-radius:7px;margin-bottom:16px;max-width:500px;font-size:14px">Пароль успешно изменён.</div>{{end}}
<div class="card" style="max-width:500px">
<form method="POST">
  {{if .NeedOld}}
  <div class="form-group">
    <label>Текущий пароль</label>
    <input type="password" name="old_password" {{if .PasswordRequired}}required{{end}} autofocus>
  </div>
  {{end}}
  <div class="form-group">
    <label>Новый пароль</label>
    <input type="password" name="new_password" {{if .PasswordRequired}}required{{end}} {{if not .NeedOld}}autofocus{{end}} minlength="{{.MinPasswordLength}}">
  </div>
  <div class="form-group">
    <label>Повторите новый пароль</label>
    <input type="password" name="confirm_password" {{if .PasswordRequired}}required{{end}} minlength="{{.MinPasswordLength}}">
  </div>
  <div style="display:flex;gap:12px;margin-top:8px">
    <button class="btn btn-primary" type="submit">Сохранить</button>
    <a class="btn" href="{{.BackURL}}" style="background:#e2e8f0;color:#475569">Отмена</a>
  </div>
</form>
</div>
{{if .SelfService}}
<div class="card" style="max-width:500px;margin-top:16px">
  <h3 style="margin-bottom:8px">Сессии</h3>
  {{if .OthersOut}}<div style="background:#f0fdf4;border:1px solid #bbf7d0;color:#16a34a;padding:10px;border-radius:7px;margin-bottom:12px;font-size:13px">Остальные сессии завершены.</div>{{end}}
  <p style="color:#64748b;font-size:13px;margin-bottom:12px">Завершить все ваши сессии на других устройствах и в других окнах. Текущее окно продолжит работать.</p>
  <form method="POST" action="/ui/profile/logout-others" data-ob-confirm="Выйти со всех устройств, кроме текущего?">
    <button class="btn" type="submit" style="background:#fee2e2;color:#b91c1c">Выйти со всех устройств</button>
  </form>
</div>
{{end}}
</main></body></html>
{{end}}`

const tplAdminSessions = `{{define "admin-sessions"}}` + adminHead + `
<main>
<div class="row-top" style="max-width:700px">
  <h2>Активные пользователи</h2>
  <a class="btn" href="/ui/admin/sessions" style="background:#e2e8f0;color:#475569;font-size:13px">Обновить</a>
</div>
{{if .AuditUsers}}
<div class="card" style="max-width:700px">
<table>
<thead><tr>
  <th>Логин</th><th>Последний запрос</th>
</tr></thead>
<tbody>
{{range .AuditUsers}}<tr>
  <td><strong>{{.Login}}</strong></td>
  <td style="font-size:12px;color:#94a3b8">{{.LastSeen.Format "02.01.2006 15:04:05"}}</td>
</tr>{{end}}
</tbody>
</table>
</div>
{{else if .NoAuth}}
<div class="card" style="max-width:700px">
  <p class="empty">Авторизация не настроена — пользователей нет.</p>
</div>
{{else if .Sessions}}
<div class="card" style="max-width:1100px;margin-bottom:16px">
  <form method="POST" action="/ui/admin/sessions/limit" style="display:flex;align-items:center;gap:10px;flex-wrap:wrap">
    <label style="font-size:13px;color:#475569">Максимум сессий на пользователя (0 — без ограничения)</label>
    <input type="number" name="limit" value="{{.Limit}}" min="0" max="99" style="width:80px">
    <button class="btn btn-sm btn-primary" type="submit">Сохранить</button>
    {{if .LimitSaved}}<span style="color:#16a34a;font-size:13px">✓ Сохранено</span>{{end}}
  </form>
  <div style="font-size:12px;color:#94a3b8;margin-top:6px">При превышении лимита новый вход вытесняет самую давнюю по активности сессию Предприятия этого пользователя. Сессии конфигуратора не учитываются.</div>
</div>
<div class="card" style="max-width:1100px">
<table>
<thead><tr>
  <th>Логин</th><th>Имя</th><th>Вид</th><th>Вход</th><th>Активность</th><th>Сессия до</th><th>IP</th><th>Браузер</th><th style="width:150px"></th>
</tr></thead>
<tbody>
{{range .Sessions}}<tr>
  <td><strong>{{.Login}}</strong>{{if .IsAdmin}} <span title="Администратор" style="color:#3b82f6">★</span>{{end}}</td>
  <td>{{.FullName}}</td>
  <td style="font-size:12px">{{.KindLabel}}</td>
  <td style="font-size:12px;color:#94a3b8">{{if .CreatedAt.IsZero}}—{{else}}{{.CreatedAt.Format "02.01 15:04"}}{{end}}</td>
  <td style="font-size:12px;color:#94a3b8">{{if .LastSeenAt.IsZero}}—{{else}}{{.LastSeenAt.Format "02.01 15:04"}}{{end}}</td>
  <td style="font-size:12px;color:#94a3b8">{{.ExpiresAt.Format "02.01.2006 15:04"}}</td>
  <td style="font-size:12px;color:#94a3b8">{{.IP}}</td>
  <td style="font-size:12px;color:#94a3b8">{{.ShortUA}}</td>
  <td>
    <div style="display:flex;gap:4px">
      {{if .PublicID}}
      <form method="POST" action="/ui/admin/sessions/kick" style="margin:0"
            data-ob-confirm="Завершить эту сессию {{.Login}}?">
        <input type="hidden" name="public_id" value="{{.PublicID}}">
        <input type="hidden" name="login" value="{{.Login}}">
        <button class="btn btn-sm btn-danger" type="submit">Завершить</button>
      </form>
      {{end}}
      <form method="POST" action="/ui/admin/sessions/{{.Login}}/kick" style="margin:0"
            data-ob-confirm="Принудительно завершить все сессии {{.Login}}?">
        <button class="btn btn-sm" type="submit" style="background:#fee2e2;color:#b91c1c" title="Завершить все сессии пользователя">Все</button>
      </form>
    </div>
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

const tplAdminAPITokens = `{{define "admin-api-tokens"}}` + adminHead + `
<main>
<div class="row-top" style="max-width:1000px">
  <h2>API-токены</h2>
  <a class="btn" href="/ui/admin/api-tokens" style="background:#e2e8f0;color:#475569;font-size:13px">Обновить</a>
</div>
{{if .Error}}<div class="error" style="max-width:1000px">{{.Error}}</div>{{end}}
{{if .CreatedToken}}
<div style="background:#f0fdf4;border:1px solid #86efac;color:#166534;padding:14px 16px;border-radius:7px;margin-bottom:16px;font-size:14px;max-width:1000px">
  <div style="font-weight:700;margin-bottom:8px">Токен создан. Скопируйте его сейчас: позже он не будет показан.</div>
  <input type="text" value="{{.CreatedToken}}" readonly data-ob-select-on-click style="font-family:monospace">
</div>
{{end}}

<div class="card" style="max-width:1000px;margin-bottom:18px">
<h3 style="margin-bottom:16px">Создать токен</h3>
{{if .Users}}
<form method="POST" action="/ui/admin/api-tokens" style="display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:12px;align-items:end">
  <div class="form-group" style="margin-bottom:0">
    <label>Название</label>
    <input type="text" name="name" required placeholder="Интеграция склада">
  </div>
  <div class="form-group" style="margin-bottom:0">
    <label>Пользователь</label>
    <select name="user_id" required>
      {{range .Users}}<option value="{{.ID}}">{{.Login}}{{if .FullName}} — {{.FullName}}{{end}}</option>{{end}}
    </select>
  </div>
  <div class="form-group" style="margin-bottom:0">
    <label>Действует до</label>
    <input type="date" name="expires_at">
  </div>
  <button class="btn btn-primary" type="submit">Создать</button>
</form>
{{else}}
<p class="empty">Сначала создайте пользователя. API-токен всегда привязан к пользователю и его ролям.</p>
{{end}}
</div>

<div class="card" style="max-width:1000px">
{{if .Tokens}}
<table>
<thead><tr>
  <th>Название</th><th>Пользователь</th><th>Создан</th><th>Последнее использование</th><th>Срок</th><th>Статус</th><th style="width:90px"></th>
</tr></thead>
<tbody>
{{range .Tokens}}<tr>
  <td><strong>{{.Name}}</strong></td>
  <td>{{.UserLogin}}</td>
  <td style="font-size:12px;color:#94a3b8">{{.CreatedAt.Format "02.01.2006 15:04"}}</td>
  <td style="font-size:12px;color:#94a3b8">{{if .LastUsedAt}}{{.LastUsedAt.Format "02.01.2006 15:04"}}{{else}}—{{end}}</td>
  <td style="font-size:12px;color:#94a3b8">{{if .ExpiresAt}}{{.ExpiresAt.Format "02.01.2006 15:04"}}{{else}}без срока{{end}}</td>
  <td>{{if .RevokedAt}}<span style="color:#dc2626">отозван</span>{{else if .Expired}}<span style="color:#f59e0b">истёк</span>{{else}}<span style="color:#16a34a">активен</span>{{end}}</td>
  <td>
    {{if .RevokedAt}}
    <span style="color:#cbd5e1">—</span>
    {{else}}
    <form method="POST" action="/ui/admin/api-tokens/{{.ID}}/revoke" data-ob-confirm="Отозвать API-токен {{.Name}}?" style="margin:0">
      <button class="btn btn-sm btn-danger" type="submit">Отозвать</button>
    </form>
    {{end}}
  </td>
</tr>{{end}}
</tbody>
</table>
{{else}}
<p class="empty">API-токенов ещё нет.</p>
{{end}}
</div>
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

// tplAdminCleanup — диагностика базы (план 114): тот же отчёт, что печатает
// `onebase doctor`. Исправления запускаются только по явно отмеченным галочкам:
// каждое из них меняет данные, поэтому «починить всё одной кнопкой» здесь нет.
const tplAdminCleanup = `{{define "admin-cleanup"}}` + adminHead + `
<main>
<h2>Диагностика базы</h2>
<p style="color:#64748b;font-size:14px;margin-bottom:20px">
  Те же проверки, что у команды <code>onebase doctor</code>. Сама по себе страница
  ничего не меняет — исправления выполняются только для отмеченных проверок.
</p>
{{if .Fixing}}
<div style="background:#f0fdf4;border:1px solid #bbf7d0;color:#16a34a;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:14px">
  Исправлено: {{.FixedTotal}}. Ниже — состояние базы после исправления.
</div>
{{end}}
<form method="POST" action="/ui/admin/cleanup"
      data-ob-confirm="Выполнить исправления для отмеченных проверок? Сначала убедитесь, что есть резервная копия.">
<div class="card" style="max-width:900px;margin-bottom:20px">
<table>
<thead><tr>
  <th style="width:34px"></th><th>Проверка</th><th>Результат</th>
</tr></thead>
<tbody>
{{range .Checks}}<tr>
  <td style="text-align:center">
    {{if and .CanFix .Findings}}<input type="checkbox" name="fix" value="{{.Check}}" title="исправить">{{end}}
  </td>
  <td style="white-space:nowrap;vertical-align:top">
    {{if eq (printf "%s" .Severity) "error"}}<span style="color:#ef4444;font-weight:600">●</span>
    {{else if eq (printf "%s" .Severity) "warn"}}<span style="color:#f59e0b;font-weight:600">●</span>
    {{else}}<span style="color:#16a34a">●</span>{{end}}
    {{.Title}}
  </td>
  <td style="vertical-align:top">
    {{.Summary}}
    {{if .Error}}<div style="color:#ef4444;font-size:13px;margin-top:4px">{{.Error}}</div>{{end}}
    {{range .Findings}}
      <div style="font-size:13px;color:#475569;margin-top:4px">
        {{.Object}}: {{.Detail}}{{if .Count}} ({{.Count}}){{end}}
        {{if .Examples}}<div style="color:#94a3b8">например: {{range $i, $e := .Examples}}{{if $i}}, {{end}}{{$e}}{{end}}</div>{{end}}
      </div>
    {{end}}
    {{if and .FixHint (not .Fixed)}}<div style="font-size:13px;color:#64748b;margin-top:4px">→ {{.FixHint}}</div>{{end}}
  </td>
</tr>{{end}}
</tbody>
</table>
</div>
{{if .Clean}}
<p class="empty" style="max-width:900px">Проблем не найдено — база в порядке.</p>
{{else}}
<button class="btn btn-danger" type="submit">Исправить отмеченное</button>
<a class="btn" href="/ui" style="background:#e2e8f0;color:#475569;margin-left:8px">Отмена</a>
<p style="color:#64748b;font-size:13px;margin-top:10px;max-width:900px">
  Исправления меняют данные. Перед запуском снимите резервную копию.
</p>
{{end}}
</form>
</main></body></html>
{{end}}`

// tplAdminWebhooks — журнал исходящих веб-хуков (план 29): последние 200
// вызовов со статусом, ошибкой, длительностью и числом попыток.
const tplAdminWebhooks = `{{define "admin-webhooks"}}` + adminHead + `
<main>
<div class="row-top" style="max-width:1100px">
  <h2>Журнал веб-хуков</h2>
</div>
<div class="card" style="max-width:1100px">
{{if .Entries}}
<table style="font-size:13px">
<thead><tr>
  <th>Время</th><th>Веб-хук</th><th>Событие</th><th>Сущность</th><th>Статус</th><th>Попыток</th><th>Длительность</th><th>Ошибка</th>
</tr></thead>
<tbody>
{{range .Entries}}
<tr>
  <td style="white-space:nowrap">{{.At.Format "02.01.2006 15:04:05"}}</td>
  <td>{{.Webhook}}</td>
  <td>{{.Event}}</td>
  <td>{{.Entity}}</td>
  <td>{{if and (ge .StatusCode 200) (lt .StatusCode 300)}}<span style="color:#16a34a">{{.StatusCode}}</span>{{else}}<span style="color:#dc2626">{{if .StatusCode}}{{.StatusCode}}{{else}}—{{end}}</span>{{end}}</td>
  <td>{{.Attempts}}</td>
  <td>{{.DurationMs}} мс</td>
  <td style="color:#dc2626;max-width:340px;overflow:hidden;text-overflow:ellipsis">{{.Error}}</td>
</tr>
{{end}}
</tbody>
</table>
{{else}}
  <p class="empty">Вызовов веб-хуков ещё не было. Веб-хуки настраиваются в config/app.yaml (блок webhooks).</p>
{{end}}
</div>
</main></body></html>
{{end}}`

const tplAdminRLS = `{{define "admin-rls"}}` + adminHead + `
<main>
<div class="row-top" style="max-width:1180px">
  <h2>Диагностика RLS</h2>
</div>

<div class="card" style="max-width:1180px;margin-bottom:20px">
<h3 style="font-size:16px;margin-bottom:12px;color:#1e293b">Row policies</h3>
{{if .Rows}}
<table style="font-size:13px">
<thead><tr>
  <th>Роль</th><th>Раздел</th><th>Объект</th><th>Операция</th><th>Field</th><th>Право</th><th>Статус</th><th>Auto-fill</th><th>Ошибка</th>
</tr></thead>
<tbody>
{{range .Rows}}
<tr>
  <td style="font-weight:600">{{.Role}}</td>
  <td>{{.Section}}</td>
  <td>{{.Kind}} {{.Object}}</td>
  <td><code>{{.Op}}</code></td>
  <td><code>{{.Field}}</code></td>
  <td>{{if .Permission}}<span style="color:#16a34a">да</span>{{else}}<span style="color:#dc2626">нет</span>{{end}}</td>
  <td>{{if eq .Status "active"}}<span style="color:#16a34a">active</span>{{else if eq .Status "ignored"}}<span style="color:#b45309">ignored</span>{{else}}<span style="color:#dc2626">{{.Status}}</span>{{end}}</td>
  <td>{{.AutoFill}}</td>
  <td style="color:#dc2626;max-width:360px">{{.Error}}</td>
</tr>
{{end}}
</tbody>
</table>
{{else}}
<p class="empty">Row policies не настроены.</p>
{{end}}
</div>

<div class="card" style="max-width:900px">
<h3 style="font-size:16px;margin-bottom:12px;color:#1e293b">DSL paths</h3>
<table style="font-size:13px">
<thead><tr><th>Путь</th><th>Режим</th><th>Поведение</th></tr></thead>
<tbody>
{{range .DSLPaths}}
<tr>
  <td style="font-weight:600">{{.Path}}</td>
  <td>{{if eq .Mode "trusted"}}<span style="color:#b45309">trusted</span>{{else}}<span style="color:#16a34a">user</span>{{end}}</td>
  <td>{{.Detail}}</td>
</tr>
{{end}}
</tbody>
</table>
</div>
</main></body></html>
{{end}}`

// adminWebhooks показывает журнал исходящих веб-хуков (только админ).
func (s *Server) adminWebhooks(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	entries, err := s.store.ListWebhookLog(r.Context(), 200)
	if err != nil {
		http.Error(w, s.errText(r, err), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderAdminTemplate(w, "admin-webhooks", map[string]any{"Entries": entries})
}
