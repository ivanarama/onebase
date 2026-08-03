package auth

// Проекция внешней учётной записи на локальную (план 84): поиск учётки по
// (провайдер, subject), связывание с уже существующей, создание новой и
// применение правил маппинга ролей.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/storage"
)

// ErrSSOUserNotFound — во внешнем провайдере вход прошёл, но локальной учётки
// нет, а автосоздание у провайдера выключено.
var ErrSSOUserNotFound = errors.New("auth: локальная учётная запись не найдена")

// UpsertSSOUser находит или заводит локальную учётку по claim'ам провайдера и
// применяет маппинг ролей. Возвращает готового пользователя для создания сессии.
func (r *Repo) UpsertSSOUser(ctx context.Context, p *OIDCProvider, claims map[string]any) (*User, error) {
	subject := claimString(claims, "sub")
	if subject == "" {
		return nil, errors.New("auth: в id_token нет sub")
	}
	login := ssoLogin(p, claims)
	if login == "" {
		return nil, fmt.Errorf("auth: провайдер не сообщил claim %q для логина", p.LoginClaimName())
	}
	fullName := claimString(claims, "name")

	user, err := r.userByAuthSubject(ctx, p.ID, subject)
	if err != nil {
		return nil, err
	}
	if user == nil {
		// Связывание с существующей локальной учёткой по логину. Почта как
		// логин связывается только подтверждённая: непроверенный email в чужом
		// каталоге — это способ представиться администратором нашей базы.
		existing, lookupErr := r.GetByLogin(ctx, login)
		if lookupErr != nil {
			return nil, lookupErr
		}
		if existing != nil {
			if strings.Contains(login, "@") && claimBoolFalse(claims, "email_verified") {
				return nil, fmt.Errorf("auth: почта %q не подтверждена провайдером", login)
			}
			if err := r.linkAuthIdentity(ctx, existing.ID, p.ID, subject); err != nil {
				return nil, err
			}
			user = existing
		}
	}
	if user == nil {
		if !p.AutoCreate {
			return nil, ErrSSOUserNotFound
		}
		created, err := r.createSSOUser(ctx, p, login, fullName, subject)
		if err != nil {
			return nil, err
		}
		user = created
	} else if fullName != "" && user.FullName != fullName {
		if err := r.updateFullName(ctx, user.ID, fullName); err != nil {
			authLog().Warn("не удалось обновить имя пользователя из SSO", "user_id", user.ID, "err", err)
		} else {
			user.FullName = fullName
		}
	}

	if err := r.applyRoleMappings(ctx, p, user, claims); err != nil {
		return nil, err
	}
	roles, err := r.GetRolesForUser(ctx, user.ID)
	if err == nil {
		user.Roles = roles
	}
	return user, nil
}

// ssoLogin выбирает логин: claim из настроек провайдера, затем обычные
// запасные варианты. sub — последнее средство: он уникален, но нечитаем.
func ssoLogin(p *OIDCProvider, claims map[string]any) string {
	for _, name := range []string{p.LoginClaimName(), "email", "preferred_username", "sub"} {
		if v := claimString(claims, name); v != "" {
			return strings.ToLower(v)
		}
	}
	return ""
}

// claimBoolFalse сообщает, что claim присутствует и равен false. Отсутствие
// claim'а не считается отрицанием: многие провайдеры его просто не выдают.
func claimBoolFalse(claims map[string]any, name string) bool {
	v, ok := claims[name]
	if !ok {
		return false
	}
	switch t := v.(type) {
	case bool:
		return !t
	case string:
		return strings.EqualFold(strings.TrimSpace(t), "false")
	}
	return false
}

// GetByLogin возвращает пользователя по логину или (nil, nil), если такого нет.
func (r *Repo) GetByLogin(ctx context.Context, login string) (*User, error) {
	d := r.db.Dialect()
	u := &User{}
	var isAdmin, denyPasswd, showInList, aiData, createdAt any
	q := fmt.Sprintf(`SELECT id, login, full_name, is_admin, deny_passwd_change, show_in_list, ai_data_access, lang, created_at
		FROM _users WHERE lower(login) = lower(%s)`, d.Placeholder(1))
	err := r.db.QueryRow(ctx, q, login).Scan(&u.ID, &u.Login, &u.FullName, &isAdmin, &denyPasswd, &showInList, &aiData, &u.Lang, &createdAt)
	if err != nil {
		if storage.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	u.IsAdmin = scanBool(isAdmin)
	u.DenyPasswdChange = scanBool(denyPasswd)
	u.ShowInList = scanBool(showInList)
	u.AIDataAccess = scanBool(aiData)
	u.CreatedAt = scanTime(createdAt)
	return u, nil
}

// userByAuthSubject ищет учётку, уже связанную с этим внешним пользователем.
func (r *Repo) userByAuthSubject(ctx context.Context, providerID, subject string) (*User, error) {
	d := r.db.Dialect()
	u := &User{}
	var isAdmin, denyPasswd, showInList, aiData, createdAt any
	q := fmt.Sprintf(`SELECT id, login, full_name, is_admin, deny_passwd_change, show_in_list, ai_data_access, lang, created_at
		FROM _users WHERE auth_provider = %s AND auth_subject = %s`, d.Placeholder(1), d.Placeholder(2))
	err := r.db.QueryRow(ctx, q, providerID, subject).Scan(&u.ID, &u.Login, &u.FullName, &isAdmin, &denyPasswd, &showInList, &aiData, &u.Lang, &createdAt)
	if err != nil {
		if storage.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	u.IsAdmin = scanBool(isAdmin)
	u.DenyPasswdChange = scanBool(denyPasswd)
	u.ShowInList = scanBool(showInList)
	u.AIDataAccess = scanBool(aiData)
	u.CreatedAt = scanTime(createdAt)
	return u, nil
}

func (r *Repo) linkAuthIdentity(ctx context.Context, userID, providerID, subject string) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`UPDATE _users SET auth_provider = %s, auth_subject = %s WHERE id = %s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
	_, err := r.db.Exec(ctx, q, providerID, subject, userID)
	return err
}

func (r *Repo) updateFullName(ctx context.Context, userID, fullName string) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`UPDATE _users SET full_name = %s WHERE id = %s`, d.Placeholder(1), d.Placeholder(2))
	_, err := r.db.Exec(ctx, q, fullName, userID)
	return err
}

// createSSOUser заводит локальную учётку под внешнего пользователя. Пароль —
// случайный и никому не известный: войти в такую учётку можно только через
// провайдера, пока администратор не назначит пароль явно.
func (r *Repo) createSSOUser(ctx context.Context, p *OIDCProvider, login, fullName, subject string) (*User, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	password := base64.RawURLEncoding.EncodeToString(buf)
	// Первый пользователь базы обязан быть администратором (инвариант
	// CreateManaged): если база пуста, вход через SSO её и заводит.
	hasUsers, err := r.HasUsers(ctx)
	if err != nil {
		return nil, err
	}
	user, err := r.Create(ctx, login, password, fullName, !hasUsers)
	if err != nil {
		return nil, err
	}
	if err := r.linkAuthIdentity(ctx, user.ID, p.ID, subject); err != nil {
		return nil, err
	}
	authLog().Info("создана учётная запись из SSO", "провайдер", p.ID, "логин", login)
	return user, nil
}

// applyRoleMappings приводит роли пользователя в соответствие правилам
// провайдера. Синхронизируются только роли, названные в правилах: роли,
// назначенные администратором вручную, провайдер не трогает — иначе вход через
// SSO молча снимал бы права, о которых каталог ничего не знает.
func (r *Repo) applyRoleMappings(ctx context.Context, p *OIDCProvider, user *User, claims map[string]any) error {
	granted, admin := p.RolesForClaims(claims)
	managed := managedRoleNames(p)
	if len(managed) == 0 && !providerManagesAdmin(p) {
		return nil
	}

	if len(managed) > 0 {
		roles, err := r.ListRoles(ctx)
		if err != nil {
			return fmt.Errorf("auth: список ролей: %w", err)
		}
		assigned, err := r.GetUserRoleIDs(ctx, user.ID)
		if err != nil {
			return fmt.Errorf("auth: роли пользователя: %w", err)
		}
		wanted := make(map[string]bool, len(granted))
		for _, name := range granted {
			wanted[strings.ToLower(name)] = true
		}
		for _, role := range roles {
			key := strings.ToLower(role.Name)
			if !managed[key] {
				continue
			}
			switch {
			case wanted[key] && !assigned[role.ID]:
				if err := r.AssignRole(ctx, user.ID, role.ID); err != nil {
					return fmt.Errorf("auth: назначение роли %s: %w", role.Name, err)
				}
			case !wanted[key] && assigned[role.ID]:
				if err := r.UnassignRole(ctx, user.ID, role.ID); err != nil {
					return fmt.Errorf("auth: снятие роли %s: %w", role.Name, err)
				}
			}
		}
		// Правило может указывать на роль, которой в базе нет: конфигурация
		// переехала, роль переименовали. Молчать нельзя — администратор увидит
		// «вошёл, но прав нет» и не поймёт причины.
		for _, name := range granted {
			if !roleExists(roles, name) {
				authLog().Warn("правило маппинга ссылается на несуществующую роль",
					"провайдер", p.ID, "роль", name)
			}
		}
	}

	if providerManagesAdmin(p) {
		if err := r.setAdminFlag(ctx, user, admin); err != nil {
			return err
		}
	}
	return nil
}

// managedRoleNames — множество ролей, которыми управляет провайдер.
func managedRoleNames(p *OIDCProvider) map[string]bool {
	managed := make(map[string]bool)
	for _, name := range p.DefaultRoles {
		if name = strings.TrimSpace(name); name != "" {
			managed[strings.ToLower(name)] = true
		}
	}
	for _, m := range p.RoleMappings {
		if name := strings.TrimSpace(m.Role); name != "" {
			managed[strings.ToLower(name)] = true
		}
	}
	return managed
}

// providerManagesAdmin — есть ли хоть одно правило, выдающее администратора.
// Только тогда провайдер вправе и снимать этот признак.
func providerManagesAdmin(p *OIDCProvider) bool {
	for _, m := range p.RoleMappings {
		if m.Admin {
			return true
		}
	}
	return false
}

func roleExists(roles []*Role, name string) bool {
	for _, role := range roles {
		if strings.EqualFold(role.Name, name) {
			return true
		}
	}
	return false
}

// setAdminFlag выставляет признак администратора по результатам маппинга.
// Последнего администратора не разжалуем: инвариант базы (ErrLastAdmin) важнее
// правил внешнего каталога, иначе неверная настройка групп оставила бы базу без
// администратора вовсе.
func (r *Repo) setAdminFlag(ctx context.Context, user *User, admin bool) error {
	if user.IsAdmin == admin {
		return nil
	}
	err := r.withUserInvariantLock(ctx, func(txCtx context.Context) error {
		if !admin {
			admins, err := r.adminCount(txCtx)
			if err != nil {
				return err
			}
			if admins <= 1 {
				return ErrLastAdmin
			}
		}
		d := r.db.Dialect()
		q := fmt.Sprintf(`UPDATE _users SET is_admin = %s WHERE id = %s`, d.Placeholder(1), d.Placeholder(2))
		_, err := r.db.Exec(txCtx, q, admin, user.ID)
		return err
	})
	if errors.Is(err, ErrLastAdmin) {
		authLog().Warn("маппинг снимает права администратора у последнего админа — признак сохранён",
			"логин", user.Login)
		return nil
	}
	if err != nil {
		return err
	}
	user.IsAdmin = admin
	return nil
}
