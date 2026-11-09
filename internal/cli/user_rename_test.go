package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/storage"
)

// onebase user rename через саму команду: логин меняется, пароль, роль и
// API-токен остаются у учётки, личная настройка переезжает, сессия
// завершается; занятый (без учёта регистра) и недопустимый логины отвергаются.
func TestUserCLI_Rename(t *testing.T) {
	dir := t.TempDir()
	rolesDir := filepath.Join(dir, "roles")
	if err := os.MkdirAll(rolesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rolesDir, "op.yaml"), []byte("name: Оператор\npermissions:\n  documents:\n    Звонок: [read]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "users.db")
	cmd := userTestCmd(t, dir, dbPath)
	mustSet(t, cmd.Flags(), "admin", "true")
	mustSet(t, cmd.Flags(), "generate", "true")
	if err := runUserAdd(cmd, []string{"admin"}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	open := func() (*storage.DB, *auth.Repo) {
		t.Helper()
		db, err := storage.ConnectSQLite(ctx, dbPath)
		if err != nil {
			t.Fatal(err)
		}
		return db, auth.NewRepo(db)
	}
	const pw = "Str0ng-Passw0rd!"
	db, repo := open()
	op, err := repo.Create(ctx, "ОператорКоллЦентраТест", pw, "Оператор", false)
	if err != nil {
		t.Fatal(err)
	}
	token, err := repo.CreateSession(ctx, op.ID, auth.SessionMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.CreateAPIToken(ctx, "callista", op.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveUserFormOpenMode(ctx, op.Login, "tabs"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := runUserRoleAssign(userTestCmd(t, dir, dbPath), []string{"ОператорКоллЦентраТест", "Оператор"}); err != nil {
		t.Fatal(err)
	}

	if err := runUserRename(userTestCmd(t, dir, dbPath), []string{"ОператорКоллЦентраТест", "Оператор_075"}); err != nil {
		t.Fatalf("переименование: %v", err)
	}

	db, repo = open()
	defer db.Close()
	if u, err := repo.Authenticate(ctx, "Оператор_075", pw); err != nil || u.ID != op.ID {
		t.Fatalf("вход под новым логином с прежним паролем: %v %+v", err, u)
	}
	if _, err := repo.Authenticate(ctx, "ОператорКоллЦентраТест", pw); err == nil {
		t.Fatal("прежний логин не должен входить")
	}
	if u, _ := repo.LookupSession(ctx, token); u != nil {
		t.Fatal("сессия учётки должна быть завершена")
	}
	roles, err := repo.GetRolesForUser(ctx, op.ID)
	if err != nil || len(roles) != 1 || roles[0].Name != "Оператор" {
		t.Fatalf("роль не сохранилась: %v %+v", err, roles)
	}
	tokens, err := repo.ListAPITokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tk := range tokens {
		if tk.UserID == op.ID && tk.RevokedAt == nil {
			found = true
		}
	}
	if !found {
		t.Fatal("API-токен учётки должен сохраниться")
	}
	if got := db.GetUserFormOpenMode(ctx, "Оператор_075"); got != "tabs" {
		t.Fatalf("личная настройка не переехала: %q", got)
	}
	if got := db.GetUserFormOpenMode(ctx, "ОператорКоллЦентраТест"); got != "" {
		t.Fatalf("у прежнего логина осталась настройка: %q", got)
	}

	if err := runUserRename(userTestCmd(t, dir, dbPath), []string{"Оператор_075", "ADMIN"}); !errors.Is(err, auth.ErrLoginTaken) {
		t.Fatalf("занятый логин (другой регистр): %v", err)
	}
	for _, bad := range []string{"оператор 075", "a/b", "x;y"} {
		if err := runUserRename(userTestCmd(t, dir, dbPath), []string{"Оператор_075", bad}); err == nil {
			t.Errorf("недопустимый логин %q принят", bad)
		}
	}
	if err := runUserRename(userTestCmd(t, dir, dbPath), []string{"нет-такого", "новый"}); err == nil {
		t.Fatal("переименование несуществующей учётки должно отказать")
	}
	// Смена только регистра у той же учётки допустима.
	if err := runUserRename(userTestCmd(t, dir, dbPath), []string{"Оператор_075", "оператор_075"}); err != nil {
		t.Fatalf("смена регистра: %v", err)
	}
}
