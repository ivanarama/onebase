package auth_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/storage"
)

func policyRepo(t *testing.T) (*auth.Repo, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "policy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	return repo, ctx
}

// Политика паролей базы задаёт минимальную длину, и она действует сразу — без
// перезапуска процесса, иначе администратор снимает ограничение и не понимает,
// почему пароль по-прежнему отвергается.
func TestStoredPolicySetsMinLength(t *testing.T) {
	repo, ctx := policyRepo(t)
	if err := repo.SaveAuthPolicy(ctx, auth.Policy{PasswordMinLength: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(ctx, "short", "abcd", "", true); err != nil {
		t.Errorf("пароль в 4 символа отвергнут при минимуме 3: %v", err)
	}
	if _, err := repo.Create(ctx, "tooshort", "ab", "", true); !errors.Is(err, auth.ErrPasswordTooShort) {
		t.Errorf("пароль в 2 символа принят при минимуме 3: %v", err)
	}
}

// Разрешение пустых паролей из интерфейса — то же самое, что переменная
// окружения, но без перезапуска: ради него всё и затевалось.
func TestStoredPolicyAllowsEmptyPassword(t *testing.T) {
	repo, ctx := policyRepo(t)
	user, err := repo.Create(ctx, "user", "Str0ng-Passw0rd!", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdatePassword(ctx, user.ID, ""); !errors.Is(err, auth.ErrPasswordRequired) {
		t.Fatalf("до включения политики пустой пароль обязан отвергаться: %v", err)
	}
	if err := repo.SaveAuthPolicy(ctx, auth.Policy{AllowEmptyPasswords: true}); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdatePassword(ctx, user.ID, ""); err != nil {
		t.Fatalf("пустой пароль отвергнут при разрешающей политике: %v", err)
	}
	if _, err := repo.Authenticate(ctx, "user", ""); err != nil {
		t.Errorf("вход с пустым паролем не работает: %v", err)
	}
}

// Длина вне диапазона игнорируется: политика базы не должна уметь ни отменить
// проверку целиком (0 и меньше), ни запретить любой пароль (больше предела
// bcrypt в 72 байта).
func TestStoredPolicyIgnoresOutOfRangeMinLength(t *testing.T) {
	for _, n := range []int{-1, 0, auth.MaxPasswordLength + 1} {
		repo, ctx := policyRepo(t)
		if err := repo.SaveAuthPolicy(ctx, auth.Policy{PasswordMinLength: n}); err != nil {
			t.Fatal(err)
		}
		if got := repo.EffectivePasswordPolicy(ctx).MinLength; got != auth.DefaultMinPasswordLength {
			t.Errorf("при PasswordMinLength=%d действующий минимум %d, ожидался умолчательный %d",
				n, got, auth.DefaultMinPasswordLength)
		}
	}
}

// Снятая галка не отменяет ONEBASE_ALLOW_EMPTY_PASSWORDS. Иначе первое же
// сохранение политики из интерфейса закрывало бы стенду вход, который он
// открыл переменной окружения ещё до появления администратора, — и вернуть
// его было бы нечем.
func TestStoredPolicyDoesNotOverrideEnvAllowEmpty(t *testing.T) {
	t.Setenv("ONEBASE_ALLOW_EMPTY_PASSWORDS", "true")
	repo, ctx := policyRepo(t)
	if err := repo.SaveAuthPolicy(ctx, auth.Policy{AllowEmptyPasswords: false, PasswordMinLength: 10}); err != nil {
		t.Fatal(err)
	}
	if !repo.EffectivePasswordPolicy(ctx).AllowEmpty {
		t.Error("сохранённая политика отменила разрешение из переменной окружения")
	}
	if _, err := repo.Create(ctx, "kiosk", "", "", true); err != nil {
		t.Errorf("пустой пароль отвергнут вопреки переменной окружения: %v", err)
	}
}
