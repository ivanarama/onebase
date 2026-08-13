package auth_test

// SEC-04 / issue #779: видимость plaintext TOTP-seed'ов и их перешифрование
// существующим мастер-ключом.

import (
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/secrets"
)

func TestMigratePlaintextTOTP(t *testing.T) {
	// Гарантируем отсутствие ключа на этапе включения 2FA → seed ляжет открытым.
	t.Setenv("ONEBASE_MASTER_KEY", "")
	t.Setenv("ONEBASE_MASTER_KEY_FILE", "")

	repo, ctx := newTestRepo(t)
	userID, secret := enable2FA(t, repo, ctx, "ivan")

	if n, err := repo.CountPlaintextTOTP(ctx); err != nil {
		t.Fatalf("CountPlaintextTOTP: %v", err)
	} else if n != 1 {
		t.Fatalf("ожидался 1 plaintext-seed, получено %d", n)
	}

	// Задаём мастер-ключ и мигрируем.
	key, err := secrets.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	t.Setenv("ONEBASE_MASTER_KEY", key.Hex())

	migrated, err := repo.MigratePlaintextTOTP(ctx)
	if err != nil {
		t.Fatalf("MigratePlaintextTOTP: %v", err)
	}
	if migrated != 1 {
		t.Fatalf("ожидалась миграция 1 seed, получено %d", migrated)
	}
	if n, err := repo.CountPlaintextTOTP(ctx); err != nil {
		t.Fatalf("CountPlaintextTOTP после миграции: %v", err)
	} else if n != 0 {
		t.Fatalf("после миграции plaintext-seed'ов быть не должно, осталось %d", n)
	}

	// Второй фактор по-прежнему работает: seed зашифрован, но расшифровывается.
	now := time.Now()
	code, err := auth.TOTPCode(secret, auth.TOTPStep(now))
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if err := repo.VerifySecondFactor(ctx, userID, code, now); err != nil {
		t.Fatalf("2FA после миграции не работает: %v", err)
	}
}
