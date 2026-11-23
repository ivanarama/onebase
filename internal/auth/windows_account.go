package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/storage"
)

// ErrAccountUnavailable covers disabled, missing and unbound users without
// disclosing which account is registered to a caller attempting to log in.
var ErrAccountUnavailable = errors.New("учётная запись недоступна для входа")

// SetWindowsAccount is the administrative binding operation. Only Windows
// resolves names. Login paths always receive a SID from a verified token.
func (r *Repo) SetWindowsAccount(ctx context.Context, userID, account string) error {
	var sid any
	if account != "" {
		domain, name, ok := strings.Cut(account, `\`)
		if !ok || domain == "" || name == "" || strings.Contains(name, `\`) {
			return errors.New(`укажите учётную запись в формате ДОМЕН\пользователь`)
		}
		resolvedSID, display, err := resolveWindowsAccount(account)
		if err != nil {
			return err
		}
		sid, account = resolvedSID, display
	}
	d := r.db.Dialect()
	q := fmt.Sprintf(`UPDATE _users SET windows_sid = %s, windows_account = %s WHERE id = %s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
	result, err := r.db.Exec(ctx, q, sid, account, userID)
	if storage.IsUniqueViolation(err) {
		return errors.New("эта учётная запись Windows уже привязана к другому пользователю")
	}
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return ErrAccountUnavailable
	}
	return nil
}

func (r *Repo) HasWindowsAccounts(ctx context.Context) (bool, error) {
	var found bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM _users WHERE windows_sid IS NOT NULL AND NOT disabled)`).Scan(&found)
	return found, err
}

func (r *Repo) GetByWindowsSID(ctx context.Context, sid string) (*User, error) {
	if sid == "" {
		return nil, ErrAccountUnavailable
	}
	q := `SELECT ` + userColumns + ` FROM _users WHERE NOT disabled AND windows_sid = ` + r.db.Dialect().Placeholder(1)
	user, err := scanUser(r.db.QueryRow(ctx, q, sid))
	if storage.IsNotFound(err) {
		return nil, ErrAccountUnavailable
	}
	return user, err
}

// CreateWindowsSession rechecks the binding at issuance, including after a
// one-time-code exchange or second-factor challenge.
func (r *Repo) CreateWindowsSession(ctx context.Context, userID, sid string, meta SessionMeta) (string, error) {
	if sid == "" || meta.Kind != SessionKindEnterprise {
		return "", ErrAccountUnavailable
	}
	return r.createSession(ctx, userID, sid, meta)
}

// SetDisabled changes login availability and revokes existing sessions in
// the same transaction. Demotion and deletion share the active-admin guard.
func (r *Repo) SetDisabled(ctx context.Context, userID string, disabled bool) error {
	return r.withUserInvariantLock(ctx, func(txCtx context.Context) error {
		u, err := r.GetByID(txCtx, userID)
		if err != nil {
			return err
		}
		if u.Disabled == disabled {
			return nil
		}
		if disabled && u.IsAdmin {
			count, err := r.adminCount(txCtx)
			if err != nil {
				return err
			}
			if count <= 1 {
				return ErrLastAdmin
			}
		}
		d := r.db.Dialect()
		q := fmt.Sprintf(`UPDATE _users SET disabled = %s WHERE id = %s`, d.Placeholder(1), d.Placeholder(2))
		if _, err := r.db.Exec(txCtx, q, disabled, userID); err != nil {
			return err
		}
		if disabled {
			return r.KickUserSessions(txCtx, userID)
		}
		return nil
	})
}
