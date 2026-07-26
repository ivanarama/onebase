package auth_test

import (
	"errors"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
)

func TestPasswordPolicyRejectsEmptyAndShortPasswordsByDefault(t *testing.T) {
	repo, ctx := newTestRepo(t)

	if _, err := repo.Create(ctx, "empty", "", "", false); !errors.Is(err, auth.ErrPasswordRequired) {
		t.Fatalf("Create(empty) error = %v, want ErrPasswordRequired", err)
	}
	if _, err := repo.Create(ctx, "short", "1234567", "", false); !errors.Is(err, auth.ErrPasswordTooShort) {
		t.Fatalf("Create(short) error = %v, want ErrPasswordTooShort", err)
	}

	user, err := repo.Create(ctx, "valid", "12345678", "", false)
	if err != nil {
		t.Fatalf("Create(valid): %v", err)
	}
	if err := repo.UpdatePassword(ctx, user.ID, ""); !errors.Is(err, auth.ErrPasswordRequired) {
		t.Fatalf("UpdatePassword(empty) error = %v, want ErrPasswordRequired", err)
	}
}

func TestPasswordPolicyAllowsExplicitKioskMode(t *testing.T) {
	t.Setenv("ONEBASE_ALLOW_EMPTY_PASSWORDS", "true")
	repo, ctx := newTestRepo(t)

	user, err := repo.Create(ctx, "kiosk", "", "", false)
	if err != nil {
		t.Fatalf("Create(empty) in kiosk mode: %v", err)
	}
	if _, err := repo.Authenticate(ctx, user.Login, ""); err != nil {
		t.Fatalf("Authenticate(empty) in kiosk mode: %v", err)
	}
}

func TestPasswordPolicySupportsExplicitMinimum(t *testing.T) {
	t.Setenv("ONEBASE_MIN_PASSWORD_LENGTH", "4")
	repo, ctx := newTestRepo(t)

	if _, err := repo.Create(ctx, "short", "pass", "", false); err != nil {
		t.Fatalf("Create with explicit four-byte minimum: %v", err)
	}
}
