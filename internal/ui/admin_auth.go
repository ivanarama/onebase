package ui

// Админка аутентификации (план 84): политики входа (обязательный второй фактор,
// запрет локальных паролей) и внешние провайдеры единого входа (OIDC).

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/secrets"
)

const tplAdminAuth = `{{define "admin-auth"}}` + adminHead + `
<main>
<h2>Аутентификация</h2>
{{if .Error}}<div class="error" style="max-width:820px">{{.Error}}</div>{{end}}
{{if .Success}}<div style="background:#f0fdf4;border:1px solid #86efac;color:#15803d;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:14px;max-width:820px">✓ {{.Success}}</div>{{end}}

<div class="card" style="max-width:820px;margin-bottom:16px">
  <h3 style="margin-bottom:14px">Политики входа</h3>
  <form method="POST" action="/ui/admin/auth/policy">
    <div class="form-group">
      <label style="display:flex;align-items:center;gap:8px;font-weight:400;cursor:pointer">
        <input type="checkbox" name="require_2fa_admins" value="1" {{if .Policy.Require2FAAdmins}}checked{{end}}> Требовать второй фактор от администраторов
      </label>
    </div>
    <div class="form-group">
      <label>Требовать второй фактор от ролей</label>
      <select name="require_2fa_roles" multiple size="{{.RoleSelectSize}}" style="width:100%;padding:8px;border:1px solid #e2e8f0;border-radius:7px">
        {{range .Roles}}<option value="{{.Name}}" {{if index $.RoleSelected .Name}}selected{{end}}>{{.Name}}</option>{{end}}
      </select>
      <div style="font-size:12px;color:#94a3b8;margin-top:4px">Учётная запись такой роли без второго фактора не войдёт: при следующем входе система потребует настроить его.</div>
    </div>
    <div class="form-group">
      <label style="display:flex;align-items:center;gap:8px;font-weight:400;cursor:pointer">
        <input type="checkbox" name="sso_only" value="1" {{if .Policy.SSOOnly}}checked{{end}}> Только единый вход (запретить локальные пароли)
      </label>
      <div style="font-size:12px;color:#dc2626;margin-top:4px">⚠ При неработающем провайдере в базу нельзя будет войти. Аварийный вход по паролю включается переменной окружения <b>ONEBASE_ALLOW_PASSWORD_LOGIN=1</b> у процесса базы. API-токены (REST v2) политике не подчиняются.</div>
    </div>
    <button class="btn btn-primary" type="submit">Сохранить политики</button>
  </form>
</div>

<div class="card" style="max-width:820px;margin-bottom:16px">
  <div class="row-top"><h3>Провайдеры единого входа</h3></div>
  {{if .Providers}}
  <table>
  <thead><tr><th>Идентификатор</th><th>Название</th><th>Issuer</th><th>Вкл.</th><th></th></tr></thead>
  <tbody>
  {{range .Providers}}<tr>
    <td><a href="/ui/admin/auth/providers/{{.ID}}" style="color:#1d4ed8;font-weight:600;text-decoration:none">{{.ID}}</a></td>
    <td>{{.Name}}</td>
    <td style="font-size:12px;color:#64748b">{{.Issuer}}</td>
    <td style="text-align:center">{{if .Enabled}}<span style="color:#16a34a;font-weight:700">✓</span>{{else}}<span style="color:#cbd5e1">—</span>{{end}}</td>
    <td>
      <form method="POST" action="/ui/admin/auth/providers/{{.ID}}/delete" data-ob-confirm="Удалить провайдера {{.ID}}?" style="margin:0">
        <button class="btn btn-sm btn-danger" type="submit">Удалить</button>
      </form>
    </td>
  </tr>{{end}}
  </tbody>
  </table>
  {{else}}
  <p class="empty">Провайдеры не настроены — вход только по локальному паролю.</p>
  {{end}}
  <div style="margin-top:14px"><a class="btn btn-primary" href="/ui/admin/auth/providers/new">+ Добавить провайдера</a></div>
</div>
</main></body></html>
{{end}}`

const tplAdminAuthProvider = `{{define "admin-auth-provider"}}` + adminHead + `
<main>
<div style="margin-bottom:16px"><a href="/ui/admin/auth" style="color:#64748b;font-size:13px;text-decoration:none">← Аутентификация</a></div>
<h2>{{if .IsNew}}Новый провайдер{{else}}Провайдер {{.P.ID}}{{end}}</h2>
{{if .Error}}<div class="error" style="max-width:720px">{{.Error}}</div>{{end}}
<div class="card" style="max-width:720px">
<form method="POST">
  <div class="form-group">
    <label>Идентификатор (в адресе возврата)</label>
    <input type="text" name="id" value="{{.P.ID}}" {{if not .IsNew}}readonly style="background:#f8fafc;color:#64748b"{{end}} placeholder="keycloak">
  </div>
  <div class="form-group">
    <label>Название кнопки на форме входа</label>
    <input type="text" name="name" value="{{.P.Name}}" placeholder="Корпоративный вход">
  </div>
  <div class="form-group">
    <label>Issuer</label>
    <input type="text" name="issuer" value="{{.P.Issuer}}" placeholder="https://id.example.com/realms/main">
    <div style="font-size:12px;color:#94a3b8;margin-top:4px">Метаданные читаются из &lt;issuer&gt;/.well-known/openid-configuration. Адрес возврата (redirect_uri) для регистрации у провайдера: <b>{{.RedirectURI}}</b></div>
  </div>
  <div class="form-group">
    <label>client_id</label>
    <input type="text" name="client_id" value="{{.P.ClientID}}">
  </div>
  <div class="form-group">
    <label>client_secret</label>
    <input type="text" name="client_secret" value="{{.SecretDisplay}}" placeholder="env:OIDC_SECRET или значение">
    <div style="font-size:12px;color:#94a3b8;margin-top:4px">Можно указать ссылку на секрет (env:ИМЯ, file:/путь) — она разыменовывается в момент обращения к провайдеру. {{if .HasMasterKey}}Введённое значение будет зашифровано мастер-ключом.{{else}}⚠ Мастер-ключ (ONEBASE_MASTER_KEY) не задан — значение ляжет в настройки открытым текстом.{{end}} Пусто = оставить прежнее.</div>
  </div>
  <div class="form-group">
    <label>Запрашиваемые scope (через пробел)</label>
    <input type="text" name="scopes" value="{{.Scopes}}" placeholder="openid email profile">
  </div>
  <div class="form-group">
    <label>Claim с логином</label>
    <input type="text" name="login_claim" value="{{.P.LoginClaim}}" placeholder="email">
  </div>
  <div class="form-group">
    <label>Роли по умолчанию (через запятую)</label>
    <input type="text" name="default_roles" value="{{.DefaultRoles}}">
  </div>
  <div class="form-group">
    <label>Правила маппинга ролей</label>
    <textarea name="role_mappings" rows="5" style="width:100%;padding:9px 12px;border:1px solid #e2e8f0;border-radius:7px;font-family:Consolas,monospace;font-size:13px">{{.RoleMappings}}</textarea>
    <div style="font-size:12px;color:#94a3b8;margin-top:4px">По строке на правило: <code>claim = значение -&gt; Роль</code>. Чтобы правило выдавало права администратора, укажите справа <code>*admin</code> (можно вместе с ролью: <code>Бухгалтер, *admin</code>). Пример: <code>groups = erp-buh -&gt; Бухгалтер</code></div>
  </div>
  <div class="form-group">
    <label style="display:flex;align-items:center;gap:8px;font-weight:400;cursor:pointer">
      <input type="checkbox" name="enabled" value="1" {{if .P.Enabled}}checked{{end}}> Включён
    </label>
  </div>
  <div class="form-group">
    <label style="display:flex;align-items:center;gap:8px;font-weight:400;cursor:pointer">
      <input type="checkbox" name="auto_create" value="1" {{if .P.AutoCreate}}checked{{end}}> Создавать учётные записи при первом входе
    </label>
  </div>
  <div class="form-group">
    <label style="display:flex;align-items:center;gap:8px;font-weight:400;cursor:pointer">
      <input type="checkbox" name="trust_mfa" value="1" {{if .P.TrustMFA}}checked{{end}}> Провайдер сам обеспечивает второй фактор
    </label>
    <div style="font-size:12px;color:#94a3b8;margin-top:4px">Без этого флага после входа через провайдера действует локальная политика второго фактора.</div>
  </div>
  <button class="btn btn-primary" type="submit">Сохранить</button>
</form>
</div>
</main></body></html>
{{end}}`

// adminAuth — /ui/admin/auth: политики и список провайдеров.
func (s *Server) adminAuth(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	s.renderAdminAuth(w, r, map[string]any{
		"Success": successFromQuery(r.URL.Query().Get("saved")),
	})
}

// successFromQuery превращает метку редиректа в текст подтверждения.
func successFromQuery(saved string) string {
	switch saved {
	case "policy":
		return "Политики сохранены"
	case "provider":
		return "Провайдер сохранён"
	case "deleted":
		return "Провайдер удалён"
	}
	return ""
}

func (s *Server) renderAdminAuth(w http.ResponseWriter, r *http.Request, data map[string]any) {
	policy := s.authRepo.AuthPolicy(r.Context())
	roles, err := s.authRepo.ListRoles(r.Context())
	if err != nil && data["Error"] == nil {
		data["Error"] = s.errText(r, err)
	}
	selected := make(map[string]bool, len(policy.Require2FARoles))
	for _, name := range policy.Require2FARoles {
		selected[name] = true
	}
	size := len(roles)
	if size < 3 {
		size = 3
	}
	if size > 10 {
		size = 10
	}
	data["Policy"] = policy
	data["Roles"] = roles
	data["RoleSelected"] = selected
	data["RoleSelectSize"] = size
	data["Providers"] = s.authRepo.AuthProviders(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderAdminTemplate(w, "admin-auth", data)
}

// adminAuthPolicySave сохраняет политики входа.
func (s *Server) adminAuthPolicySave(w http.ResponseWriter, r *http.Request) {
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
	policy := auth.Policy{
		SSOOnly:          r.FormValue("sso_only") == "1",
		Require2FAAdmins: r.FormValue("require_2fa_admins") == "1",
		Require2FARoles:  r.Form["require_2fa_roles"],
	}
	// Запрет паролей без единственного работающего способа войти — верный
	// способ запереть базу. Провайдеров должно быть хотя бы одно включённое.
	if policy.SSOOnly && len(s.authRepo.EnabledAuthProviders(r.Context())) == 0 {
		s.renderAdminAuth(w, r, map[string]any{
			"Error": "Нельзя запретить локальные пароли, пока нет ни одного включённого провайдера единого входа",
		})
		return
	}
	if err := s.authRepo.SaveAuthPolicy(r.Context(), policy); err != nil {
		s.renderAdminAuth(w, r, map[string]any{"Error": s.errText(r, err)})
		return
	}
	s.logSessionAudit(r, "auth_policy_saved", "", "")
	http.Redirect(w, r, "/ui/admin/auth?saved=policy", http.StatusFound)
}

// adminAuthProvider — карточка провайдера (GET — форма, POST — сохранение).
func (s *Server) adminAuthProvider(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	isNew := id == "new"
	var current *auth.OIDCProvider
	if !isNew {
		p, ok := s.authRepo.AuthProvider(r.Context(), id)
		if !ok {
			http.NotFound(w, r)
			return
		}
		current = p
	} else {
		current = &auth.OIDCProvider{Enabled: true, AutoCreate: false}
	}

	if r.Method == http.MethodPost {
		s.limitMultipartRequest(w, r)
		if err := parseBoundedForm(r, defaultFormMemoryBytes); err != nil {
			http.Error(w, s.errText(r, err), uploadErrorStatus(err))
			return
		}
		updated, err := s.providerFromForm(r, current, isNew)
		if err != nil {
			s.renderProviderForm(w, r, updated, isNew, s.errText(r, err))
			return
		}
		if err := s.saveProvider(r, updated, isNew); err != nil {
			s.renderProviderForm(w, r, updated, isNew, s.errText(r, err))
			return
		}
		s.logSessionAudit(r, "auth_provider_saved", updated.ID, "")
		http.Redirect(w, r, "/ui/admin/auth?saved=provider", http.StatusFound)
		return
	}
	s.renderProviderForm(w, r, current, isNew, "")
}

// providerFromForm собирает провайдера из полей формы.
func (s *Server) providerFromForm(r *http.Request, current *auth.OIDCProvider, isNew bool) (*auth.OIDCProvider, error) {
	p := &auth.OIDCProvider{
		ID:           strings.TrimSpace(r.FormValue("id")),
		Name:         strings.TrimSpace(r.FormValue("name")),
		Issuer:       strings.TrimRight(strings.TrimSpace(r.FormValue("issuer")), "/"),
		ClientID:     strings.TrimSpace(r.FormValue("client_id")),
		ClientSecret: current.ClientSecret,
		Scopes:       strings.Fields(r.FormValue("scopes")),
		LoginClaim:   strings.TrimSpace(r.FormValue("login_claim")),
		Enabled:      r.FormValue("enabled") == "1",
		AutoCreate:   r.FormValue("auto_create") == "1",
		TrustMFA:     r.FormValue("trust_mfa") == "1",
		DefaultRoles: splitList(r.FormValue("default_roles")),
	}
	if !isNew {
		p.ID = current.ID
	}
	if p.Name == "" {
		p.Name = p.ID
	}
	mappings, err := parseRoleMappings(r.FormValue("role_mappings"))
	if err != nil {
		return p, err
	}
	p.RoleMappings = mappings

	// Пустое поле секрета означает «не менять»: иначе редактирование любого
	// другого поля стирало бы секрет, которого в форме и не видно.
	if raw := strings.TrimSpace(r.FormValue("client_secret")); raw != "" && !secrets.IsRef(raw) {
		if key, kerr := secrets.Default().Key(); kerr == nil {
			enc, encErr := key.Encrypt(raw)
			if encErr != nil {
				return p, fmt.Errorf("шифрование секрета: %w", encErr)
			}
			p.ClientSecret = enc
		} else {
			p.ClientSecret = raw
		}
	} else if raw != "" {
		p.ClientSecret = raw
	}
	return p, p.Validate()
}

// saveProvider добавляет или заменяет провайдера в списке.
func (s *Server) saveProvider(r *http.Request, p *auth.OIDCProvider, isNew bool) error {
	providers := s.authRepo.AuthProviders(r.Context())
	replaced := false
	for i, existing := range providers {
		if strings.EqualFold(existing.ID, p.ID) {
			if isNew {
				return fmt.Errorf("провайдер %q уже существует", p.ID)
			}
			providers[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		providers = append(providers, p)
	}
	return s.authRepo.SaveAuthProviders(r.Context(), providers)
}

// adminAuthProviderDelete удаляет провайдера.
func (s *Server) adminAuthProviderDelete(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	providers := s.authRepo.AuthProviders(r.Context())
	kept := make([]*auth.OIDCProvider, 0, len(providers))
	for _, p := range providers {
		if !strings.EqualFold(p.ID, id) {
			kept = append(kept, p)
		}
	}
	// Удаление последнего провайдера при sso_only заперло бы базу — снимаем
	// политику вместе с ним, чтобы вход остался возможен.
	policy := s.authRepo.AuthPolicy(r.Context())
	if policy.SSOOnly && !anyEnabled(kept) {
		policy.SSOOnly = false
		if err := s.authRepo.SaveAuthPolicy(r.Context(), policy); err != nil {
			http.Error(w, s.errText(r, err), http.StatusInternalServerError)
			return
		}
		s.logSessionAudit(r, "auth_policy_sso_only_released", id, "")
	}
	if err := s.authRepo.SaveAuthProviders(r.Context(), kept); err != nil {
		http.Error(w, s.errText(r, err), http.StatusInternalServerError)
		return
	}
	s.logSessionAudit(r, "auth_provider_deleted", id, "")
	http.Redirect(w, r, "/ui/admin/auth?saved=deleted", http.StatusFound)
}

func anyEnabled(providers []*auth.OIDCProvider) bool {
	for _, p := range providers {
		if p.Enabled {
			return true
		}
	}
	return false
}

func (s *Server) renderProviderForm(w http.ResponseWriter, r *http.Request, p *auth.OIDCProvider, isNew bool, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderAdminTemplate(w, "admin-auth-provider", map[string]any{
		"P":             p,
		"IsNew":         isNew,
		"Error":         errMsg,
		"Scopes":        strings.Join(p.ScopeList(), " "),
		"DefaultRoles":  strings.Join(p.DefaultRoles, ", "),
		"RoleMappings":  formatRoleMappings(p.RoleMappings),
		"SecretDisplay": secretDisplay(p.ClientSecret),
		"HasMasterKey":  secrets.Default().HasKey(),
		"RedirectURI":   providerRedirectURI(r, p.ID),
	})
}

// secretDisplay показывает ссылку на секрет как есть, а сам секрет — никогда.
func secretDisplay(value string) string {
	if value == "" {
		return ""
	}
	if secrets.IsRef(value) && !strings.HasPrefix(value, "enc:") {
		return value
	}
	return ""
}

// providerRedirectURI — подсказка администратору, что регистрировать у
// провайдера. Совпадает с тем, что отправляет auth.Handlers.
func providerRedirectURI(r *http.Request, id string) string {
	if id == "" {
		id = "<идентификатор>"
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = strings.TrimSpace(strings.Split(proto, ",")[0])
	}
	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		host = strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	return scheme + "://" + host + "/auth/oidc/" + id + "/callback"
}

// splitList разбирает список через запятую.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// adminRoleToken — маркер «выдать права администратора» в правиле маппинга.
const adminRoleToken = "*admin"

// parseRoleMappings разбирает строки вида «claim = значение -> Роль, *admin».
func parseRoleMappings(text string) ([]auth.OIDCRoleMapping, error) {
	var out []auth.OIDCRoleMapping
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		left, right, ok := strings.Cut(line, "->")
		if !ok {
			return nil, fmt.Errorf("строка %d: ожидалось «claim = значение -> Роль»", i+1)
		}
		claim, value, _ := strings.Cut(left, "=")
		claim = strings.TrimSpace(claim)
		if claim == "" {
			return nil, fmt.Errorf("строка %d: не указан claim", i+1)
		}
		m := auth.OIDCRoleMapping{Claim: claim, Value: strings.TrimSpace(value)}
		for _, target := range splitList(right) {
			if strings.EqualFold(target, adminRoleToken) {
				m.Admin = true
				continue
			}
			if m.Role != "" {
				// Несколько ролей в одном правиле — это несколько правил;
				// разворачиваем, чтобы не терять ни одну.
				out = append(out, m)
				m = auth.OIDCRoleMapping{Claim: m.Claim, Value: m.Value}
			}
			m.Role = target
		}
		if m.Role == "" && !m.Admin {
			return nil, fmt.Errorf("строка %d: не указана роль", i+1)
		}
		out = append(out, m)
	}
	return out, nil
}

// formatRoleMappings — обратное преобразование для формы.
func formatRoleMappings(mappings []auth.OIDCRoleMapping) string {
	var b strings.Builder
	for _, m := range mappings {
		target := m.Role
		if m.Admin {
			if target != "" {
				target += ", "
			}
			target += adminRoleToken
		}
		fmt.Fprintf(&b, "%s = %s -> %s\n", m.Claim, m.Value, target)
	}
	return b.String()
}
