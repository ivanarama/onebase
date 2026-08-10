package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/storage"
)

// Политика, требующая второй фактор от администраторов, при выключенной
// самопривязке запирает базу насовсем: привязка на входе просит одноразовый код
// от администратора, а выдать его некому — администратор сам войти не может.
// Продуктового выхода не было вовсе, только правка _settings сырым SQL.
//
// Хуже: в это состояние вводила команда `onebase user 2fa reset`, которую
// справка и предупреждение при старте называли средством ВОССТАНОВЛЕНИЯ
// доступа (#615, хвост #620).

func userTestBase(t *testing.T) (string, string) {
	t.Helper()
	return t.TempDir(), filepath.Join(t.TempDir(), "users.db")
}

func setupAdminWithTOTP(t *testing.T, dir, dbPath string) string {
	t.Helper()
	cmd := userTestCmd(t, dir, dbPath)
	mustSet(t, cmd.Flags(), "admin", "true")
	mustSet(t, cmd.Flags(), "generate", "true")
	if err := runUserAdd(cmd, []string{"admin"}); err != nil {
		t.Fatalf("создание админа: %v", err)
	}
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := findUserByLogin(ctx, repo, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.EnableTOTP(ctx, u.ID, "JBSWY3DPEHPK3PXP", 0); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveAuthPolicy(ctx, auth.Policy{Require2FAAdmins: true}); err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func policyOf(t *testing.T, dbPath string) auth.Policy {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	return auth.NewRepo(db).AuthPolicy(ctx)
}

// Снятие фактора у последнего администратора, который его привязал, обязано
// быть отклонено: иначе база запирается, а команда рапортует об успехе.
func TestUser2FAReset_ОтказываетсяЗапиратьБазу(t *testing.T) {
	dir, dbPath := userTestBase(t)
	userID := setupAdminWithTOTP(t, dir, dbPath)

	cmd := userTestCmd(t, dir, dbPath)
	err := runUser2FAReset(cmd, []string{"admin"})
	if err == nil {
		t.Fatal("сброс последнего привязанного администратора должен быть отклонён")
	}
	if !strings.Contains(err.Error(), "self-enroll on") {
		t.Errorf("в отказе нет выхода из тупика: %v", err)
	}

	ctx := context.Background()
	db, _ := storage.ConnectSQLite(ctx, dbPath)
	defer db.Close()
	if on, err := auth.NewRepo(db).TOTPEnabled(ctx, userID); err != nil || !on {
		t.Errorf("фактор всё-таки снят: on=%v err=%v", on, err)
	}
}

// --force остаётся: учётная запись может быть недоступна физически, и тогда
// администратор осознанно идёт на блокировку, чтобы разбираться дальше.
func TestUser2FAReset_ForceСнимаетНесмотряНаРиск(t *testing.T) {
	dir, dbPath := userTestBase(t)
	userID := setupAdminWithTOTP(t, dir, dbPath)

	user2FAResetForce = true
	t.Cleanup(func() { user2FAResetForce = false })

	cmd := userTestCmd(t, dir, dbPath)
	if err := runUser2FAReset(cmd, []string{"admin"}); err != nil {
		t.Fatalf("с --force сброс должен проходить: %v", err)
	}
	ctx := context.Background()
	db, _ := storage.ConnectSQLite(ctx, dbPath)
	defer db.Close()
	if on, err := auth.NewRepo(db).TOTPEnabled(ctx, userID); err != nil || on {
		t.Errorf("фактор не снят: on=%v err=%v", on, err)
	}
}

// Продуктовый выход из уже запертой базы: самопривязка включается офлайн, а
// само требование второго фактора остаётся в силе.
func TestUser2FASelfEnroll_ВыводитИзБлокировки(t *testing.T) {
	dir, dbPath := userTestBase(t)
	setupAdminWithTOTP(t, dir, dbPath)
	// Приводим базу в запертое состояние тем же способом, что и в проде.
	user2FAResetForce = true
	cmd := userTestCmd(t, dir, dbPath)
	if err := runUser2FAReset(cmd, []string{"admin"}); err != nil {
		t.Fatalf("подготовка: %v", err)
	}
	user2FAResetForce = false

	cmd = userTestCmd(t, dir, dbPath)
	if err := runUser2FASelfEnroll(cmd, []string{"on"}); err != nil {
		t.Fatalf("self-enroll on: %v", err)
	}
	p := policyOf(t, dbPath)
	if !p.SelfEnroll2FA {
		t.Error("самопривязка не включена")
	}
	if !p.Require2FAAdmins {
		t.Error("требование второго фактора снято — команда не должна ослаблять политику")
	}

	// Обратное переключение работает.
	cmd = userTestCmd(t, dir, dbPath)
	if err := runUser2FASelfEnroll(cmd, []string{"off"}); err != nil {
		t.Fatalf("self-enroll off: %v", err)
	}
	if policyOf(t, dbPath).SelfEnroll2FA {
		t.Error("самопривязка не выключена обратно")
	}
}

func TestUser2FASelfEnroll_НепонятныйАргумент(t *testing.T) {
	dir, dbPath := userTestBase(t)
	setupAdminWithTOTP(t, dir, dbPath)
	cmd := userTestCmd(t, dir, dbPath)
	if err := runUser2FASelfEnroll(cmd, []string{"maybe"}); err == nil {
		t.Fatal("непонятный аргумент должен давать ошибку")
	}
}
