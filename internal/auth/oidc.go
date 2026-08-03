package auth

// Внешние провайдеры входа (SSO, план 84): описание провайдера, его хранение в
// _settings и правила отображения claim'ов на локальные учётки и роли.
//
// Протокол — OpenID Connect, поток Authorization Code + PKCE (см. oidc_client.go).
// Реализация на stdlib: разбор id_token и проверка подписи по JWKS — это около
// двухсот строк, а зависимость в контуре аутентификации пришлось бы отдельно
// держать под vuln-сканом (план 109).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/secrets"
)

// authProvidersKey — ключ _settings со списком провайдеров.
const authProvidersKey = "auth.providers"

// OIDCProvider — настройки одного внешнего провайдера.
type OIDCProvider struct {
	// ID участвует в URL (/auth/oidc/<id>/start) — короткий латинский слаг.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Issuer — базовый URL провайдера; из него берётся
	// <issuer>/.well-known/openid-configuration.
	Issuer   string `json:"issuer"`
	ClientID string `json:"client_id"`
	// ClientSecret — значение или ссылка на секрет (env:/file:/enc:, план 83).
	// Разыменовывается в момент обмена кода на токен, а не при чтении настроек,
	// чтобы расшифрованный секрет не оседал в describe и бэкапах.
	ClientSecret string   `json:"client_secret,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	Enabled      bool     `json:"enabled"`
	// AutoCreate разрешает заводить локальную учётку при первом входе. Без него
	// пускаются только те, кому учётку уже создал администратор.
	AutoCreate bool `json:"auto_create,omitempty"`
	// LoginClaim — claim, из которого берётся логин локальной учётки
	// (email | preferred_username | sub). Пусто = email.
	LoginClaim string `json:"login_claim,omitempty"`
	// RoleMappings — правила «claim содержит значение → роль».
	RoleMappings []OIDCRoleMapping `json:"role_mappings,omitempty"`
	// DefaultRoles назначаются всем, кто вошёл через провайдера.
	DefaultRoles []string `json:"default_roles,omitempty"`
	// TrustMFA — провайдер сам обеспечивает второй фактор. Тогда после входа
	// через него локальный шаг 2FA не запрашивается. Выключено по умолчанию:
	// иначе включение SSO молча снимало бы требование политики.
	TrustMFA bool `json:"trust_mfa,omitempty"`
}

// OIDCRoleMapping — одно правило маппинга. Value сравнивается без учёта
// регистра; claim-массивы (обычный вид groups/roles) проверяются поэлементно.
type OIDCRoleMapping struct {
	Claim string `json:"claim"`
	Value string `json:"value"`
	Role  string `json:"role,omitempty"`
	// Admin выдаёт признак администратора вместо (или вместе с) роли.
	Admin bool `json:"admin,omitempty"`
}

// DefaultOIDCScopes — минимальный набор: openid обязателен по спецификации,
// email/profile нужны для логина и отображаемого имени.
var DefaultOIDCScopes = []string{"openid", "email", "profile"}

// ScopeList возвращает запрашиваемые scope'ы с гарантированным openid.
func (p *OIDCProvider) ScopeList() []string {
	scopes := p.Scopes
	if len(scopes) == 0 {
		scopes = DefaultOIDCScopes
	}
	hasOpenID := false
	for _, s := range scopes {
		if strings.EqualFold(strings.TrimSpace(s), "openid") {
			hasOpenID = true
			break
		}
	}
	if !hasOpenID {
		scopes = append([]string{"openid"}, scopes...)
	}
	return scopes
}

// LoginClaimName — claim логина с подстановкой значения по умолчанию.
func (p *OIDCProvider) LoginClaimName() string {
	if c := strings.TrimSpace(p.LoginClaim); c != "" {
		return c
	}
	return "email"
}

// Validate проверяет заполненность обязательных полей — до записи в настройки,
// а не в момент входа: администратор должен узнать об опечатке сразу.
func (p *OIDCProvider) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return errors.New("auth: у провайдера не задан идентификатор")
	}
	if !validProviderID(p.ID) {
		return fmt.Errorf("auth: идентификатор провайдера %q: допустимы латинские буквы, цифры, дефис и подчёркивание", p.ID)
	}
	if strings.TrimSpace(p.Issuer) == "" {
		return errors.New("auth: у провайдера не задан issuer")
	}
	if err := checkIssuerScheme(p.Issuer); err != nil {
		return err
	}
	if strings.TrimSpace(p.ClientID) == "" {
		return errors.New("auth: у провайдера не задан client_id")
	}
	return nil
}

// validProviderID ограничивает слаг: он попадает в путь URL, и «/» или «..» в
// нём означали бы совсем другой маршрут.
func validProviderID(id string) bool {
	for _, ch := range id {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9', ch == '-', ch == '_':
		default:
			return false
		}
	}
	return true
}

// AuthProviders читает список провайдеров. Как и политики, деградирует к
// пустому списку: сломанная настройка SSO не должна ронять форму входа.
func (r *Repo) AuthProviders(ctx context.Context) []*OIDCProvider {
	if r == nil || r.db == nil {
		return nil
	}
	raw, ok, err := r.db.GetSetting(ctx, authProvidersKey)
	if err != nil {
		authLog().Warn("не удалось прочитать список провайдеров входа", "err", err)
		return nil
	}
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	var providers []*OIDCProvider
	if err := json.Unmarshal([]byte(raw), &providers); err != nil {
		authLog().Error("битый JSON провайдеров входа — SSO отключено",
			"ключ", authProvidersKey, "err", err)
		return nil
	}
	return providers
}

// EnabledAuthProviders — только включённые провайдеры (кнопки на форме входа).
func (r *Repo) EnabledAuthProviders(ctx context.Context) []*OIDCProvider {
	var out []*OIDCProvider
	for _, p := range r.AuthProviders(ctx) {
		if p.Enabled {
			out = append(out, p)
		}
	}
	return out
}

// AuthProvider ищет провайдера по идентификатору.
func (r *Repo) AuthProvider(ctx context.Context, id string) (*OIDCProvider, bool) {
	for _, p := range r.AuthProviders(ctx) {
		if strings.EqualFold(p.ID, id) {
			return p, true
		}
	}
	return nil, false
}

// SaveAuthProviders записывает список провайдеров целиком.
func (r *Repo) SaveAuthProviders(ctx context.Context, providers []*OIDCProvider) error {
	seen := make(map[string]bool, len(providers))
	for _, p := range providers {
		if err := p.Validate(); err != nil {
			return err
		}
		key := strings.ToLower(p.ID)
		if seen[key] {
			return fmt.Errorf("auth: провайдер %q объявлен дважды", p.ID)
		}
		seen[key] = true
	}
	raw, err := json.Marshal(providers) //nolint:gosec // G117: client_secret сериализуется намеренно — в _settings он и хранится, причём ссылкой (env:/file:/enc:), см. ResolvedClientSecret
	if err != nil {
		return fmt.Errorf("auth: сериализация провайдеров: %w", err)
	}
	return r.db.SaveSetting(ctx, authProvidersKey, string(raw))
}

// ResolvedClientSecret разыменовывает ссылку на секрет провайдера.
func (p *OIDCProvider) ResolvedClientSecret() (string, error) {
	if strings.TrimSpace(p.ClientSecret) == "" {
		return "", nil
	}
	v, err := secrets.Default().Resolve(p.ClientSecret)
	if err != nil {
		return "", fmt.Errorf("auth: секрет провайдера %s: %w", p.ID, err)
	}
	return v, nil
}

// RolesForClaims применяет правила маппинга к claim'ам id_token и возвращает
// имена ролей и признак администратора.
func (p *OIDCProvider) RolesForClaims(claims map[string]any) (roles []string, admin bool) {
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		for _, existing := range roles {
			if strings.EqualFold(existing, name) {
				return
			}
		}
		roles = append(roles, name)
	}
	for _, role := range p.DefaultRoles {
		add(role)
	}
	for _, m := range p.RoleMappings {
		if !claimMatches(claims, m.Claim, m.Value) {
			continue
		}
		add(m.Role)
		if m.Admin {
			admin = true
		}
	}
	return roles, admin
}

// claimMatches проверяет, содержит ли claim нужное значение. Значение claim'а
// бывает строкой, списком строк (groups) или числом/булевым — приводим к строке
// и сравниваем без учёта регистра. Пустое ожидаемое значение означает «claim
// присутствует и не пуст» (например, правило на сам факт наличия признака).
func claimMatches(claims map[string]any, claim, want string) bool {
	claim = strings.TrimSpace(claim)
	if claim == "" {
		return false
	}
	v, ok := claims[claim]
	if !ok || v == nil {
		return false
	}
	want = strings.TrimSpace(want)
	values := claimStrings(v)
	if want == "" {
		return len(values) > 0
	}
	for _, s := range values {
		if strings.EqualFold(strings.TrimSpace(s), want) {
			return true
		}
	}
	return false
}

// claimStrings приводит значение claim'а к списку строк.
func claimStrings(v any) []string {
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case bool:
		if t {
			return []string{"true"}
		}
		return []string{"false"}
	case float64:
		return []string{strings.TrimSuffix(fmt.Sprintf("%v", t), ".0")}
	case []any:
		var out []string
		for _, item := range t {
			out = append(out, claimStrings(item)...)
		}
		return out
	case []string:
		return t
	}
	return nil
}

// claimString достаёт строковое значение claim'а (логин, имя, почта).
func claimString(claims map[string]any, name string) string {
	values := claimStrings(claims[name])
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}
