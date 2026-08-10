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
	// SelfEnroll2FA разрешает первичную привязку второго фактора прямо на входе
	// по одному паролю. Ноль-значение (выключено) — безопасное умолчание: без
	// него принудительная привязка отдавала бы второй фактор любому, кто
	// предъявил пароль, — для утёкшего пароля это закрепление доступа, после
	// которого владелец не войдёт (issue #577). Тогда привязка на входе требует
	// одноразового кода от администратора (IssueBindTicket, карточка
	// пользователя). Флаг включают там, где 2FA раздают массово и самопривязка
	// удобнее. На вход через SSO не влияет: провайдер уже подтвердил личность.
	SelfEnroll2FA bool `json:"self_enroll_2fa,omitempty"`
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

// TwoFactorLockoutRisk сообщает, запрёт ли включение этой политики базу: требуем
// второй фактор от когорты, у которой ни у кого он не привязан, а самопривязка
// (SelfEnroll2FA) выключена — тогда первичная привязка на входе требует кода от
// администратора, а выдать его некому. Возвращает человекочитаемое имя первой
// такой когорты («администраторов», «роли X»), либо "" если политика безопасна.
//
// Зеркалит защиту SSOOnly в adminAuthPolicySave: включить требование без
// единственного работающего способа войти — верный способ запереть базу (#620).
func (r *Repo) TwoFactorLockoutRisk(ctx context.Context, p Policy) (string, error) {
	return r.twoFactorLockoutRisk(ctx, p, "")
}

// TwoFactorLockoutRiskAfterDisable отвечает на вопрос «а если снять второй
// фактор у этого пользователя — не запрём ли мы базу?».
//
// Нужен потому, что `onebase user 2fa reset` умеет ВОГНАТЬ в тупик: сняв фактор
// у последнего администратора, который его привязал, команда оставляет политику
// требующей второй фактор, а привязать его на входе больше некому. Справка
// команды при этом называет её средством восстановления доступа (#615, хвост
// #620).
func (r *Repo) TwoFactorLockoutRiskAfterDisable(ctx context.Context, p Policy, userID string) (string, error) {
	return r.twoFactorLockoutRisk(ctx, p, userID)
}

// excludeUser — «как если бы у этого пользователя фактор был снят»: он остаётся
// в когорте, но перестаёт считаться привязанным.
func (r *Repo) twoFactorLockoutRisk(ctx context.Context, p Policy, excludeUserID string) (string, error) {
	// Самопривязка ломает тупик: любой введёт пароль и привяжет фактор сам.
	if p.SelfEnroll2FA {
		return "", nil
	}
	// Администраторы — невосстановимый случай: если заперты они, выдать код
	// привязки некому вообще. (Роли админ ещё мог бы разлочить, будь он в базе.)
	if p.Require2FAAdmins {
		total, err := r.countUsers(ctx, "WHERE is_admin")
		if err != nil {
			return "", err
		}
		bound, err := r.countBoundAdmins(ctx, excludeUserID)
		if err != nil {
			return "", err
		}
		if total > 0 && bound == 0 {
			return "администраторов", nil
		}
	}
	for _, role := range p.Require2FARoles {
		role = strings.TrimSpace(role)
		if role == "" {
			continue
		}
		total, err := r.countRoleMembers(ctx, role, false)
		if err != nil {
			return "", err
		}
		bound, err := r.countBoundRoleMembers(ctx, role, excludeUserID)
		if err != nil {
			return "", err
		}
		if total > 0 && bound == 0 {
			return "роли «" + role + "»", nil
		}
	}
	return "", nil
}

// countUsers считает учётки по условию (bool-колонки в WHERE годятся для обоих
// диалектов: PG boolean, SQLite integer 0/1).
// countBoundAdmins — администраторы с привязанным вторым фактором. excludeUserID
// вычитается из подсчёта: так проверяется «что будет, если снять фактор у него».
func (r *Repo) countBoundAdmins(ctx context.Context, excludeUserID string) (int, error) {
	if excludeUserID == "" {
		return r.countUsers(ctx, "WHERE is_admin AND totp_enabled")
	}
	d := r.db.Dialect()
	var n int
	err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM _users WHERE is_admin AND totp_enabled AND id <> `+d.Placeholder(1),
		excludeUserID).Scan(&n)
	return n, err
}

// countBoundRoleMembers — то же для членов роли.
func (r *Repo) countBoundRoleMembers(ctx context.Context, roleName, excludeUserID string) (int, error) {
	if excludeUserID == "" {
		return r.countRoleMembers(ctx, roleName, true)
	}
	d := r.db.Dialect()
	var n int
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM _users u
		JOIN _user_roles ur ON ur.user_id = u.id
		JOIN _roles rl ON rl.id = ur.role_id
		WHERE rl.name = `+d.Placeholder(1)+` AND u.totp_enabled AND u.id <> `+d.Placeholder(2),
		roleName, excludeUserID).Scan(&n)
	return n, err
}

func (r *Repo) countUsers(ctx context.Context, where string) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM _users `+where).Scan(&n)
	return n, err
}

// countRoleMembers считает членов роли по имени; withTOTP — только с привязанным
// вторым фактором.
func (r *Repo) countRoleMembers(ctx context.Context, roleName string, withTOTP bool) (int, error) {
	d := r.db.Dialect()
	q := `SELECT COUNT(*) FROM _users u
		JOIN _user_roles ur ON ur.user_id = u.id
		JOIN _roles rl ON rl.id = ur.role_id
		WHERE rl.name = ` + d.Placeholder(1)
	if withTOTP {
		q += ` AND u.totp_enabled`
	}
	var n int
	err := r.db.QueryRow(ctx, q, roleName).Scan(&n)
	return n, err
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
