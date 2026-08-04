package auth_test

// Второй фактор на реальной SQLite-базе (план 84): включение, одноразовость
// кодов, резервные коды, шифрование секрета мастер-ключом.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
)

// enable2FA заводит пользователя с включённым вторым фактором и возвращает его
// идентификатор и секрет.
func enable2FA(t *testing.T, repo *auth.Repo, ctx context.Context, login string) (string, string) {
	t.Helper()
	user, err := repo.Create(ctx, login, "S3cret-pass", "Тест", true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	if err := repo.EnableTOTP(ctx, user.ID, secret, 0); err != nil {
		t.Fatalf("EnableTOTP: %v", err)
	}
	return user.ID, secret
}

func TestVerifySecondFactorAcceptsCodeOnce(t *testing.T) {
	repo, ctx := newTestRepo(t)
	userID, secret := enable2FA(t, repo, ctx, "ivan")

	now := time.Now()
	code, err := auth.TOTPCode(secret, auth.TOTPStep(now))
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if err := repo.VerifySecondFactor(ctx, userID, code, now); err != nil {
		t.Fatalf("первый ввод кода должен приниматься: %v", err)
	}
	// Тот же код второй раз — отказ: перехваченный код действовал бы все 30
	// секунд своего шага.
	if err := repo.VerifySecondFactor(ctx, userID, code, now); err == nil {
		t.Fatal("переигранный код принят повторно")
	}
}

func TestVerifySecondFactorRejectsExpiredCode(t *testing.T) {
	repo, ctx := newTestRepo(t)
	userID, secret := enable2FA(t, repo, ctx, "ivan")

	now := time.Now()
	stale, err := auth.TOTPCode(secret, auth.TOTPStep(now.Add(-5*time.Minute)))
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if err := repo.VerifySecondFactor(ctx, userID, stale, now); err == nil {
		t.Fatal("просроченный код принят")
	}
}

func TestBackupCodesAreSingleUse(t *testing.T) {
	repo, ctx := newTestRepo(t)
	userID, _ := enable2FA(t, repo, ctx, "ivan")

	codes, err := repo.ReplaceBackupCodes(ctx, userID)
	if err != nil {
		t.Fatalf("ReplaceBackupCodes: %v", err)
	}
	if len(codes) < 5 {
		t.Fatalf("ожидался комплект резервных кодов, получено %d", len(codes))
	}
	info, err := repo.TwoFactorInfoFor(ctx, userID)
	if err != nil {
		t.Fatalf("TwoFactorInfoFor: %v", err)
	}
	if !info.Enabled || info.BackupCodesLeft != len(codes) {
		t.Fatalf("состояние 2FA: %+v, ожидалось enabled=true, кодов %d", info, len(codes))
	}

	if err := repo.VerifySecondFactor(ctx, userID, codes[0], time.Now()); err != nil {
		t.Fatalf("резервный код должен приниматься: %v", err)
	}
	if err := repo.VerifySecondFactor(ctx, userID, codes[0], time.Now()); err == nil {
		t.Fatal("резервный код сработал дважды")
	}
	// Регистр и дефис при вводе роли не играют.
	if err := repo.VerifySecondFactor(ctx, userID, strings.ToUpper(codes[1]), time.Now()); err != nil {
		t.Fatalf("резервный код в верхнем регистре отвергнут: %v", err)
	}
	info, err = repo.TwoFactorInfoFor(ctx, userID)
	if err != nil {
		t.Fatalf("TwoFactorInfoFor: %v", err)
	}
	if info.BackupCodesLeft != len(codes)-2 {
		t.Fatalf("осталось кодов %d, ожидалось %d", info.BackupCodesLeft, len(codes)-2)
	}
}

func TestReplaceBackupCodesInvalidatesPrevious(t *testing.T) {
	repo, ctx := newTestRepo(t)
	userID, _ := enable2FA(t, repo, ctx, "ivan")

	old, err := repo.ReplaceBackupCodes(ctx, userID)
	if err != nil {
		t.Fatalf("ReplaceBackupCodes: %v", err)
	}
	if _, err := repo.ReplaceBackupCodes(ctx, userID); err != nil {
		t.Fatalf("ReplaceBackupCodes (повтор): %v", err)
	}
	if err := repo.VerifySecondFactor(ctx, userID, old[0], time.Now()); err == nil {
		t.Fatal("код из отменённого комплекта всё ещё работает")
	}
}

func TestDisableTOTPClearsSecretAndCodes(t *testing.T) {
	repo, ctx := newTestRepo(t)
	userID, secret := enable2FA(t, repo, ctx, "ivan")
	codes, err := repo.ReplaceBackupCodes(ctx, userID)
	if err != nil {
		t.Fatalf("ReplaceBackupCodes: %v", err)
	}

	if err := repo.DisableTOTP(ctx, userID); err != nil {
		t.Fatalf("DisableTOTP: %v", err)
	}
	enabled, err := repo.TOTPEnabled(ctx, userID)
	if err != nil {
		t.Fatalf("TOTPEnabled: %v", err)
	}
	if enabled {
		t.Fatal("второй фактор остался включённым")
	}
	info, err := repo.TwoFactorInfoFor(ctx, userID)
	if err != nil {
		t.Fatalf("TwoFactorInfoFor: %v", err)
	}
	if info.BackupCodesLeft != 0 {
		t.Fatalf("резервные коды не удалены: осталось %d", info.BackupCodesLeft)
	}
	if err := repo.VerifySecondFactor(ctx, userID, codes[0], time.Now()); err == nil {
		t.Fatal("резервный код работает после отключения 2FA")
	}
	code, err := auth.TOTPCode(secret, auth.TOTPStep(time.Now()))
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if err := repo.VerifySecondFactor(ctx, userID, code, time.Now()); err == nil {
		t.Fatal("код прежнего секрета работает после отключения 2FA")
	}
}

func TestEnableTOTPEncryptsSecretWithMasterKey(t *testing.T) {
	t.Setenv("ONEBASE_MASTER_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	repo, db, ctx := newTestRepoDB(t)
	userID, secret := enable2FA(t, repo, ctx, "ivan")

	var stored string
	if err := db.QueryRow(ctx, `SELECT totp_secret FROM _users WHERE id = ?`, userID).Scan(&stored); err != nil {
		t.Fatalf("чтение totp_secret: %v", err)
	}
	if !strings.HasPrefix(stored, "enc:") {
		t.Fatalf("секрет лежит не зашифрованным: %q", stored)
	}
	if strings.Contains(stored, secret) {
		t.Fatal("секрет виден в сохранённом значении открытым текстом")
	}
	// Зашифрованный секрет обязан оставаться рабочим.
	now := time.Now()
	code, err := auth.TOTPCode(secret, auth.TOTPStep(now))
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if err := repo.VerifySecondFactor(ctx, userID, code, now); err != nil {
		t.Fatalf("код не принят при зашифрованном секрете: %v", err)
	}
}
