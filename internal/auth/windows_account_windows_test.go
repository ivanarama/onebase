package auth_test

import (
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestBindCurrentWindowsUser(t *testing.T) {
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	require.NoError(t, err)
	account, domain, kind, err := tokenUser.User.Sid.LookupAccount("")
	require.NoError(t, err)
	if kind != windows.SidTypeUser {
		t.Skip("process is running under a service identity")
	}
	name := domain + `\` + account
	sid := tokenUser.User.Sid.String()
	repo, ctx := newTestRepo(t)
	user, err := repo.CreateManaged(ctx, "local", "Password-123", "", true)
	require.NoError(t, err)
	other, err := repo.CreateManaged(ctx, "other", "Password-123", "", false)
	require.NoError(t, err)
	require.NoError(t, repo.SetWindowsAccount(ctx, user.ID, name))
	require.Error(t, repo.SetWindowsAccount(ctx, other.ID, name))
	require.Error(t, repo.SetWindowsAccount(ctx, user.ID, account))
	mapped, err := repo.GetByWindowsSID(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, user.ID, mapped.ID)
	require.Equal(t, name, mapped.WindowsAccount)
	require.NoError(t, repo.SetWindowsAccount(ctx, user.ID, ""))
	_, err = repo.GetByWindowsSID(ctx, sid)
	require.ErrorIs(t, err, auth.ErrAccountUnavailable)
	require.NoError(t, repo.SetWindowsAccount(ctx, other.ID, name))
	mapped, err = repo.GetByWindowsSID(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, other.ID, mapped.ID)
}
