package auth

// Политики аутентификации (план 84). Свойство ИНСТАНСА базы, а не поставляемой
// конфигурации: требование второго фактора и запрет локальных паролей заводит
// администратор конкретной установки, и переезжать вместе с .obz они не должны.
// Поэтому _settings, как у лимита сессий (план 78), а не app.yaml.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// authPolicyKey — ключ _settings с политиками входа.
const authPolicyKey = "auth.policy"

// envAllowPasswordLogin — аварийный обход `sso_only`. Провайдер OIDC может
// лечь, истечь сертификатом или быть настроен с опечаткой — и тогда в базу
// нельзя войти вообще никак, включая администратора, который бы это починил.
// Переменная окружения доступна тому, кто управляет процессом базы, то есть
// уже имеет доступ к её файлам: она снимает удобство, а не защиту.
const envAllowPasswordLogin = "ONEBASE_ALLOW_PASSWORD_LOGIN" //nolint:gosec // G101: это имя переменной окружения, а не пароль

// Policy — политики входа.
type Policy struct {
	// SSOOnly запрещает вход по локальному паролю: только через внешнего
	// провайдера. API-токены (план 26) политике не подчиняются — это отдельный
	// канал для сервисов, у которого нет ни пароля, ни второго фактора.
	SSOOnly bool `json:"sso_only,omitempty"`
	// Require2FAAdmins требует второй фактор от всех администраторов.
	Require2FAAdmins bool `json:"require_2fa_admins,omitempty"`
	// Require2FARoles — роли, которым второй фактор обязателен.
	Require2FARoles []string `json:"require_2fa_roles,omitempty"`
}

// Enabled сообщает, задана ли хоть одна политика. Пустая политика — это
// поведение версий до плана 84.
func (p Policy) Enabled() bool {
	return p.SSOOnly || p.Require2FAAdmins || len(p.Require2FARoles) > 0
}

// RequiresTwoFactor решает, обязателен ли второй фактор этому пользователю.
// Роли должны быть уже загружены (см. Repo.RequiresTwoFactor).
func (p Policy) RequiresTwoFactor(u *User) bool {
	if u == nil {
		return false
	}
	if p.Require2FAAdmins && u.IsAdmin {
		return true
	}
	for _, want := range p.Require2FARoles {
		want = strings.TrimSpace(want)
		if want == "" {
			continue
		}
		for _, role := range u.Roles {
			if strings.EqualFold(role.Name, want) {
				return true
			}
		}
	}
	return false
}

// PasswordLoginAllowed сообщает, разрешён ли вход по локальному паролю.
func (p Policy) PasswordLoginAllowed() bool {
	return !p.SSOOnly || passwordLoginBreakGlass()
}

// passwordLoginBreakGlass — включён ли аварийный обход sso_only.
func passwordLoginBreakGlass() bool {
	v, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(envAllowPasswordLogin)))
	return err == nil && v
}

// AuthPolicy читает политики. Отсутствие ключа, битый JSON и недоступная
// таблица дают пустую политику: аутентификация обязана работать и на базе,
// созданной до плана 84.
func (r *Repo) AuthPolicy(ctx context.Context) Policy {
	if r == nil || r.db == nil {
		return Policy{}
	}
	raw, ok, err := r.db.GetSetting(ctx, authPolicyKey)
	if err != nil || !ok || strings.TrimSpace(raw) == "" {
		if err != nil {
			authLog().Warn("не удалось прочитать политики аутентификации", "err", err)
		}
		return Policy{}
	}
	var p Policy
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		authLog().Error("битый JSON политик аутентификации — политики не применяются",
			"ключ", authPolicyKey, "err", err)
		return Policy{}
	}
	return p
}

// SaveAuthPolicy сохраняет политики.
func (r *Repo) SaveAuthPolicy(ctx context.Context, p Policy) error {
	cleaned := make([]string, 0, len(p.Require2FARoles))
	for _, role := range p.Require2FARoles {
		if role = strings.TrimSpace(role); role != "" {
			cleaned = append(cleaned, role)
		}
	}
	p.Require2FARoles = cleaned
	raw, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("auth: сериализация политик: %w", err)
	}
	return r.db.SaveSetting(ctx, authPolicyKey, string(raw))
}

// RequiresTwoFactor — версия Policy.RequiresTwoFactor, которая сама догружает
// роли пользователя. Сбой загрузки ролей трактуется как «второй фактор нужен»:
// иначе временная недоступность таблицы ролей снимала бы политику ровно тогда,
// когда база ведёт себя странно.
func (r *Repo) RequiresTwoFactor(ctx context.Context, p Policy, u *User) bool {
	if !p.Require2FAAdmins && len(p.Require2FARoles) == 0 {
		return false
	}
	if u == nil {
		return false
	}
	if p.Require2FAAdmins && u.IsAdmin {
		return true
	}
	if len(p.Require2FARoles) == 0 {
		return false
	}
	if u.Roles == nil {
		roles, err := r.GetRolesForUser(ctx, u.ID)
		if err != nil {
			authLog().Error("не удалось загрузить роли для политики 2FA — требуем второй фактор",
				"user_id", u.ID, "err", err)
			return true
		}
		u.Roles = roles
	}
	return p.RequiresTwoFactor(u)
}
