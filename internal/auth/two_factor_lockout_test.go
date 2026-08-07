package auth_test

import (
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
)

// #620: включить требование второго фактора от когорты, у которой он ни у кого не
// привязан, при выключенной самопривязке — значит запереть базу навсегда
// (первичная привязка на входе требует кода от администратора, а выдать его
// некому). TwoFactorLockoutRisk обязана распознать этот случай — на нём стоит
// guard в adminAuthPolicySave и предупреждение при старте.
func TestTwoFactorLockoutRisk(t *testing.T) {
	repo, ctx := newTestRepo(t)

	admin, err := repo.Create(ctx, "admin", "S3cret-pass-1", "Админ", true)
	if err != nil {
		t.Fatal(err)
	}

	// Админ без привязанного 2FA + require_2fa_admins + самопривязка выкл → тупик.
	cohort, err := repo.TwoFactorLockoutRisk(ctx, auth.Policy{Require2FAAdmins: true})
	if err != nil {
		t.Fatal(err)
	}
	if cohort != "администраторов" {
		t.Fatalf("тупик по администраторам не распознан: %q", cohort)
	}

	// Самопривязка ломает тупик: любой войдёт по паролю и привяжется сам.
	if c, err := repo.TwoFactorLockoutRisk(ctx, auth.Policy{Require2FAAdmins: true, SelfEnroll2FA: true}); err != nil || c != "" {
		t.Fatalf("самопривязка снимает риск: cohort=%q err=%v", c, err)
	}

	// Привязали второй фактор администратору — тупика больше нет.
	if err := repo.EnableTOTP(ctx, admin.ID, "JBSWY3DPEHPK3PXP", 0); err != nil {
		t.Fatal(err)
	}
	if c, err := repo.TwoFactorLockoutRisk(ctx, auth.Policy{Require2FAAdmins: true}); err != nil || c != "" {
		t.Fatalf("после привязки 2FA риска быть не должно: cohort=%q err=%v", c, err)
	}

	// Роль: член без привязанного 2FA + require по этой роли → тупик по роли.
	role := &auth.Role{Name: "Кассир"}
	if err := repo.SyncRoles(ctx, []*auth.Role{role}); err != nil {
		t.Fatal(err)
	}
	cashier, err := repo.Create(ctx, "kassir", "S3cret-pass-2", "Кассир", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AssignRole(ctx, cashier.ID, role.ID); err != nil {
		t.Fatal(err)
	}
	c, err := repo.TwoFactorLockoutRisk(ctx, auth.Policy{Require2FARoles: []string{"Кассир"}})
	if err != nil {
		t.Fatal(err)
	}
	if c != "роли «Кассир»" {
		t.Fatalf("тупик по роли не распознан: %q", c)
	}

	// Роль без единственного члена (require по несуществующей роли) риска не несёт.
	if c, err := repo.TwoFactorLockoutRisk(ctx, auth.Policy{Require2FARoles: []string{"НетТакойРоли"}}); err != nil || c != "" {
		t.Fatalf("пустая роль не должна давать риск: cohort=%q err=%v", c, err)
	}
}
