package auth_test

import (
	"fmt"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestWindowsBindingAndDisabledAccount(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := t.Context()
		repo := auth.NewRepo(db)
		require.NoError(t, repo.EnsureSchema(ctx))
		admin, err := repo.CreateManaged(ctx, "admin", "Password-123", "", true)
		require.NoError(t, err)
		user, err := repo.CreateManaged(ctx, "operator", "Password-456", "", false)
		require.NoError(t, err)
		checkAvailable := func(want bool) {
			t.Helper()
			available, err := repo.HasWindowsAccounts(ctx)
			require.NoError(t, err)
			require.Equal(t, want, available)
		}
		checkAvailable(false)
		const sid = "S-1-5-21-111-222-333-1001"
		// Seed an already-resolved SID in this cross-platform fixture. The
		// Windows-only test binds a real account using the public setter.
		d := db.Dialect()
		bind := fmt.Sprintf(`UPDATE _users SET windows_sid = %s, windows_account = %s WHERE id = %s`, d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
		_, err = db.Exec(ctx, bind, sid, `EXAMPLE\operator`, user.ID)
		require.NoError(t, err)
		_, err = db.Exec(ctx, bind, sid, `EXAMPLE\operator`, admin.ID)
		require.True(t, storage.IsUniqueViolation(err))
		require.NoError(t, repo.EnsureSchema(ctx))
		checkAvailable(true)
		found, err := repo.GetByWindowsSID(ctx, sid)
		require.NoError(t, err)
		require.Equal(t, user.ID, found.ID)
		require.False(t, found.IsAdmin)
		meta := auth.SessionMeta{Kind: auth.SessionKindEnterprise}
		for _, wrong := range []string{"", `EXAMPLE\operator`, sid + "0"} {
			_, err = repo.GetByWindowsSID(ctx, wrong)
			require.ErrorIs(t, err, auth.ErrAccountUnavailable)
			_, err = repo.CreateWindowsSession(ctx, user.ID, wrong, meta)
			require.ErrorIs(t, err, auth.ErrAccountUnavailable)
		}
		_, err = repo.CreateWindowsSession(ctx, admin.ID, sid, meta)
		require.ErrorIs(t, err, auth.ErrAccountUnavailable)
		require.NoError(t, db.SaveMaxSessionsPerUser(ctx, 1))
		session, err := repo.CreateWindowsSession(ctx, user.ID, sid, meta)
		require.NoError(t, err)
		_, err = repo.CreateWindowsSession(ctx, user.ID, sid+"0", meta)
		require.ErrorIs(t, err, auth.ErrAccountUnavailable)
		_, err = repo.LookupSessionKind(ctx, session, meta.Kind)
		require.NoError(t, err)
		_, rawAPI, err := repo.CreateAPIToken(ctx, "integration", user.ID, nil)
		require.NoError(t, err)
		require.NoError(t, repo.SetDisabled(ctx, user.ID, true))
		checkAvailable(false)
		_, err = repo.Authenticate(ctx, user.Login, "Password-456")
		require.Error(t, err)
		_, err = repo.GetByWindowsSID(ctx, sid)
		require.ErrorIs(t, err, auth.ErrAccountUnavailable)
		_, err = repo.CreateWindowsSession(ctx, user.ID, sid, meta)
		require.ErrorIs(t, err, auth.ErrAccountUnavailable)
		_, err = repo.CreateSession(ctx, user.ID, meta)
		require.ErrorIs(t, err, auth.ErrAccountUnavailable)
		_, err = repo.LookupAPIToken(ctx, rawAPI)
		require.Error(t, err)
		require.NoError(t, repo.SetDisabled(ctx, user.ID, false))
		_, err = repo.LookupSessionKind(ctx, session, meta.Kind)
		require.Error(t, err)
		checkAvailable(true)

		require.NoError(t, repo.SetWindowsAccount(ctx, user.ID, ""))
		checkAvailable(false)
		_, err = repo.CreateWindowsSession(ctx, user.ID, sid, meta)
		require.ErrorIs(t, err, auth.ErrAccountUnavailable)
		_, err = repo.Authenticate(ctx, user.Login, "Password-456")
		require.NoError(t, err)

		// A disabled administrator cannot recover the base in place of
		// the last active administrator.
		require.NoError(t, repo.Update(ctx, user.ID, "", true, false, false, false))
		require.NoError(t, repo.SetDisabled(ctx, user.ID, true))
		require.ErrorIs(t, repo.SetDisabled(ctx, admin.ID, true), auth.ErrLastAdmin)
		require.ErrorIs(t, repo.Update(ctx, admin.ID, "", false, false, false, false), auth.ErrLastAdmin)
		require.ErrorIs(t, repo.Delete(ctx, admin.ID), auth.ErrLastAdmin)
	})
}

func TestMigrateExistingUsersToWindowsBindings(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := t.Context()
		repo := auth.NewRepo(db)
		require.NoError(t, repo.EnsureSchema(ctx))
		user, err := repo.CreateManaged(ctx, "legacy", "Password-123", "", true)
		require.NoError(t, err)
		meta := auth.SessionMeta{Kind: auth.SessionKindEnterprise}
		session, err := repo.CreateSession(ctx, user.ID, meta)
		require.NoError(t, err)
		// Reproduce the previous schema with a populated account and session.
		for _, ddl := range []string{
			"DROP INDEX ix_users_windows_sid",
			"ALTER TABLE _users DROP COLUMN windows_sid",
			"ALTER TABLE _users DROP COLUMN windows_account",
			"ALTER TABLE _users DROP COLUMN disabled",
		} {
			_, err := db.Exec(ctx, ddl)
			require.NoError(t, err)
		}
		require.NoError(t, repo.EnsureSchema(ctx))
		found, err := repo.GetByID(ctx, user.ID)
		require.NoError(t, err)
		require.False(t, found.Disabled)
		require.Empty(t, found.WindowsAccount)
		available, err := repo.HasWindowsAccounts(ctx)
		require.NoError(t, err)
		require.False(t, available)
		_, err = repo.Authenticate(ctx, user.Login, "Password-123")
		require.NoError(t, err)
		_, err = repo.LookupSessionKind(ctx, session, meta.Kind)
		require.NoError(t, err)
	})
}

func TestConcurrentCappedEnterpriseLogins(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := t.Context()
		repo := auth.NewRepo(db)
		require.NoError(t, repo.EnsureSchema(ctx))
		user, err := repo.CreateManaged(ctx, "limited", "Password-123", "", true)
		require.NoError(t, err)
		require.NoError(t, db.SaveMaxSessionsPerUser(ctx, 1))
		start := make(chan struct{})
		type login struct {
			token string
			err   error
		}
		const clients = 4
		results := make(chan login, clients)
		meta := auth.SessionMeta{Kind: auth.SessionKindEnterprise}
		for range clients {
			go func() {
				<-start
				token, err := repo.CreateSession(ctx, user.ID, meta)
				results <- login{token: token, err: err}
			}()
		}
		close(start)
		logins := make([]login, clients)
		for i := range logins {
			logins[i] = <-results
		}
		live := 0
		for _, login := range logins {
			require.NoError(t, login.err)
			_, err := repo.LookupSessionKind(ctx, login.token, meta.Kind)
			if err == nil {
				live++
			} else {
				require.True(t, storage.IsNotFound(err), "session lookup: %v", err)
			}
		}
		require.Equal(t, 1, live)
	})
}
