package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	// ErrLoginTaken — новый логин уже занят другой учёткой (без учёта
	// регистра: вход по SSO ищет учётку именно так).
	ErrLoginTaken = errors.New("логин уже занят другим пользователем")
	// ErrSameLogin — переименование в тот же логин.
	ErrSameLogin = errors.New("новый логин совпадает с прежним")
)

const maxLoginRunes = 64

// ValidateLogin проверяет новый логин: буквы любого алфавита (кириллица
// тоже), цифры и «_ . - @», без пробелов, до 64 символов. «Оператор_075» и
// «y.s.lagutin» допустимы.
func ValidateLogin(login string) error {
	if login == "" {
		return errors.New("логин не задан")
	}
	if !utf8.ValidString(login) {
		return errors.New("логин: недопустимая кодировка")
	}
	if utf8.RuneCountInString(login) > maxLoginRunes {
		return fmt.Errorf("логин длиннее %d символов", maxLoginRunes)
	}
	for _, r := range login {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_.-@", r) {
			continue
		}
		return fmt.Errorf("логин: недопустимый символ %q — можно буквы, цифры и «_ . - @»", r)
	}
	return nil
}

// RenameUser переименовывает учётку oldLogin в newLogin одной транзакцией:
//   - _users.login;
//   - личные настройки и варианты отчётов, хранящиеся по логину строкой
//     (storage.RenameUserData);
//   - все сессии учётки завершаются: в них записан прежний логин, и войти
//     нужно заново.
//
// Пароль, второй фактор, резервные коды, роли и API-токены привязаны к id и
// сохраняются. Журналы не меняются: они история. Данные конфигурации, где
// логин записан строкой (например, регистры сведений), платформа не знает —
// их переносит сама конфигурация.
func (r *Repo) RenameUser(ctx context.Context, oldLogin, newLogin string) (*User, error) {
	if err := ValidateLogin(newLogin); err != nil {
		return nil, err
	}
	if oldLogin == newLogin {
		return nil, ErrSameLogin
	}
	var u *User
	err := r.withUserInvariantLock(ctx, func(txCtx context.Context) error {
		got, err := r.renameInTx(txCtx, oldLogin, newLogin)
		u = got
		return err
	})
	if err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateAndRename — сохранение карточки учётки одним действием: смена логина
// (если newLogin задан и отличается) и остальные поля — под одной блокировкой
// и в одной транзакции. Отказ любой части (логин занят, последний
// администратор) не применяет ничего: раньше логин успевал смениться, сессии —
// завершиться, а сохранение карточки отказывало.
func (r *Repo) UpdateAndRename(ctx context.Context, userID, oldLogin, newLogin, fullName string, isAdmin, denyPasswdChange, showInList, aiDataAccess bool) (renamed *User, err error) {
	rename := newLogin != "" && newLogin != oldLogin
	if rename {
		if err := ValidateLogin(newLogin); err != nil {
			return nil, err
		}
	}
	err = r.withUserInvariantLock(ctx, func(txCtx context.Context) error {
		if rename {
			u, err := r.renameInTx(txCtx, oldLogin, newLogin)
			if err != nil {
				return err
			}
			if u.ID != userID {
				return fmt.Errorf("пользователь %q не найден", oldLogin)
			}
			renamed = u
		}
		return r.updateInTx(txCtx, userID, fullName, isAdmin, denyPasswdChange, showInList, aiDataAccess)
	})
	if err != nil {
		return nil, err
	}
	return renamed, nil
}

// renameInTx — тело переименования; вызывать под withUserInvariantLock.
func (r *Repo) renameInTx(txCtx context.Context, oldLogin, newLogin string) (*User, error) {
	d := r.db.Dialect()
	var u *User
	err := func() error {
		var id string
		q := fmt.Sprintf(`SELECT id FROM _users WHERE login = %s`, d.Placeholder(1))
		if err := r.db.QueryRow(txCtx, q, oldLogin).Scan(&id); err != nil {
			return fmt.Errorf("пользователь %q не найден", oldLogin)
		}
		// Занятость — без учёта регистра и для не-ASCII (кириллица): встроенная
		// lower() SQLite меняет только ASCII, и «оператор» проходил мимо
		// «Оператор». LowerLike даёт ob_lower на SQLite и LOWER на PostgreSQL.
		var taken int
		q = fmt.Sprintf(`SELECT count(*) FROM _users WHERE %s = %s AND id <> %s`, d.LowerLike("login"), d.LowerLike(d.Placeholder(1)), d.Placeholder(2))
		if err := r.db.QueryRow(txCtx, q, newLogin, id).Scan(&taken); err != nil {
			return err
		}
		if taken > 0 {
			return ErrLoginTaken
		}
		q = fmt.Sprintf(`UPDATE _users SET login = %s WHERE id = %s`, d.Placeholder(1), d.Placeholder(2))
		if _, err := r.db.Exec(txCtx, q, newLogin, id); err != nil {
			return fmt.Errorf("auth: rename user: %w", err)
		}
		if err := r.db.RenameUserData(txCtx, oldLogin, newLogin); err != nil {
			return err
		}
		if _, err := r.db.Exec(txCtx, `DELETE FROM _sessions WHERE user_id = `+d.Placeholder(1), id); err != nil {
			return fmt.Errorf("auth: rename user: sessions: %w", err)
		}
		got, err := r.GetByID(txCtx, id)
		if err != nil {
			return err
		}
		u = got
		return nil
	}()
	if err != nil {
		return nil, err
	}
	return u, nil
}
