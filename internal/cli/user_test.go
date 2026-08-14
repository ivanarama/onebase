package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

// userTestCmd собирает команду со всеми флагами, которые читают RunE-функции
// user-подкоманд (базовые + пароль/имя/админ), с выбранными project и sqlite.
func userTestCmd(t *testing.T, projectDir, dbPath string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	fs := cmd.Flags()
	fs.String("id", "", "")
	fs.String("project", ".", "")
	fs.String("sqlite", "", "")
	fs.String("db", "", "")
	fs.String("name", "", "")
	fs.Bool("admin", false, "")
	fs.Bool("show-in-list", false, "")
	fs.Bool("on", false, "")
	fs.Bool("off", false, "")
	fs.Bool("generate", false, "")
	fs.Bool("password-stdin", false, "")
	mustSet(t, fs, "project", projectDir)
	mustSet(t, fs, "sqlite", dbPath)
	return cmd
}

func mustSet(t *testing.T, fs interface{ Set(string, string) error }, name, val string) {
	t.Helper()
	if err := fs.Set(name, val); err != nil {
		t.Fatalf("set %s=%s: %v", name, val, err)
	}
}

func TestUserCLI_AddListRoleInvariants(t *testing.T) {
	dir := t.TempDir()
	rolesDir := filepath.Join(dir, "roles")
	if err := os.MkdirAll(rolesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	role := "name: Кладовщик\npermissions:\n  documents:\n    Реализация: [read, post]\n"
	if err := os.WriteFile(filepath.Join(rolesDir, "warehouse.yaml"), []byte(role), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "users.db")

	// Первый пользователь обязан быть администратором.
	cmd := userTestCmd(t, dir, dbPath)
	mustSet(t, cmd.Flags(), "generate", "true")
	if err := runUserAdd(cmd, []string{"klad"}); err == nil {
		t.Fatal("первый пользователь без --admin должен быть отклонён (ErrFirstUserMustBeAdmin)")
	}

	// Админ создаётся.
	cmd = userTestCmd(t, dir, dbPath)
	mustSet(t, cmd.Flags(), "admin", "true")
	mustSet(t, cmd.Flags(), "generate", "true")
	if err := runUserAdd(cmd, []string{"admin"}); err != nil {
		t.Fatalf("создание админа: %v", err)
	}

	// Теперь можно завести обычного пользователя.
	cmd = userTestCmd(t, dir, dbPath)
	mustSet(t, cmd.Flags(), "generate", "true")
	if err := runUserAdd(cmd, []string{"klad"}); err != nil {
		t.Fatalf("создание пользователя: %v", err)
	}

	// Дубликат логина отклоняется (UNIQUE login).
	cmd = userTestCmd(t, dir, dbPath)
	mustSet(t, cmd.Flags(), "generate", "true")
	if err := runUserAdd(cmd, []string{"klad"}); err == nil {
		t.Fatal("повторный логин должен быть отклонён")
	}

	// Назначение роли из roles/*.yaml.
	cmd = userTestCmd(t, dir, dbPath)
	if err := runUserRoleAssign(cmd, []string{"klad", "Кладовщик"}); err != nil {
		t.Fatalf("назначение роли: %v", err)
	}

	// Проверяем состояние через репозиторий.
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := auth.NewRepo(db)

	users, err := repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("ожидалось 2 пользователя, получено %d", len(users))
	}
	u, err := findUserByLogin(ctx, repo, "klad")
	if err != nil {
		t.Fatal(err)
	}
	roles, err := repo.GetRolesForUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 1 || roles[0].Name != "Кладовщик" {
		t.Fatalf("ожидалась роль Кладовщик, получено %+v", roles)
	}

	// Снятие роли.
	cmd = userTestCmd(t, dir, dbPath)
	if err := runUserRoleRevoke(cmd, []string{"klad", "Кладовщик"}); err != nil {
		t.Fatalf("снятие роли: %v", err)
	}
	roles, _ = repo.GetRolesForUser(ctx, u.ID)
	if len(roles) != 0 {
		t.Fatalf("роль должна быть снята, осталось %+v", roles)
	}

	// Нельзя удалить последнего администратора.
	cmd = userTestCmd(t, dir, dbPath)
	if err := runUserRm(cmd, []string{"admin"}); err == nil {
		t.Fatal("удаление последнего админа должно быть отклонено (ErrLastAdmin)")
	}
}

// TestUserCLI_AddShowInList проверяет, что --show-in-list при add выставляет
// show_in_list=true (пользователь попадает в reference-пикеры ListForSelection),
// а без флага — нет. Это ядро дефекта: раньше созданный из CLI пользователь
// никогда не появлялся в списках выбора.
func TestUserCLI_AddShowInList(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "users.db")

	// Первый пользователь — админ, без --show-in-list.
	cmd := userTestCmd(t, dir, dbPath)
	mustSet(t, cmd.Flags(), "admin", "true")
	mustSet(t, cmd.Flags(), "generate", "true")
	if err := runUserAdd(cmd, []string{"admin"}); err != nil {
		t.Fatalf("создание админа: %v", err)
	}

	// Обычный пользователь с --show-in-list.
	cmd = userTestCmd(t, dir, dbPath)
	mustSet(t, cmd.Flags(), "generate", "true")
	mustSet(t, cmd.Flags(), "show-in-list", "true")
	if err := runUserAdd(cmd, []string{"klad"}); err != nil {
		t.Fatalf("создание пользователя: %v", err)
	}

	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := auth.NewRepo(db)

	// В списках выбора — только klad; admin создан без флага.
	sel, err := repo.ListForSelection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sel) != 1 || sel[0].Login != "klad" {
		t.Fatalf("ожидался только klad в списке выбора, получено %+v", sel)
	}

	// Флаг корректно проставлен/не проставлен на самих пользователях.
	klad, err := findUserByLogin(ctx, repo, "klad")
	if err != nil {
		t.Fatal(err)
	}
	if !klad.ShowInList {
		t.Fatal("show_in_list должен быть true у klad (--show-in-list передавался)")
	}
	admin, err := findUserByLogin(ctx, repo, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if admin.ShowInList {
		t.Fatal("show_in_list должен быть false у admin (флаг не передавался)")
	}
}

// TestUserCLI_ShowInListToggle проверяет подкоманду show-in-list --on/--off:
// включение/выключение видимости уже существующего пользователя в reference-
// пикерах, валидацию взаимоисключающих флагов и ошибку на неизвестном логине.
func TestUserCLI_ShowInListToggle(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "users.db")

	// admin (первый — админ) + обычный пользователь без show-in-list.
	cmd := userTestCmd(t, dir, dbPath)
	mustSet(t, cmd.Flags(), "admin", "true")
	mustSet(t, cmd.Flags(), "generate", "true")
	if err := runUserAdd(cmd, []string{"admin"}); err != nil {
		t.Fatalf("создание админа: %v", err)
	}
	cmd = userTestCmd(t, dir, dbPath)
	mustSet(t, cmd.Flags(), "generate", "true")
	if err := runUserAdd(cmd, []string{"klad"}); err != nil {
		t.Fatalf("создание пользователя: %v", err)
	}

	// Без флагов — отказ (не трогаем видимость без явного намерения).
	cmd = userTestCmd(t, dir, dbPath)
	err := runUserShowInList(cmd, []string{"klad"})
	if err == nil || !strings.Contains(err.Error(), "--on или --off") {
		t.Fatalf("без флагов ожидалась ошибка про --on или --off, получено %v", err)
	}
	// Оба флага — отказ (противоречие).
	cmd = userTestCmd(t, dir, dbPath)
	mustSet(t, cmd.Flags(), "on", "true")
	mustSet(t, cmd.Flags(), "off", "true")
	err = runUserShowInList(cmd, []string{"klad"})
	if err == nil || !strings.Contains(err.Error(), "взаимоисключающи") {
		t.Fatalf("с обоими флагами ожидалась ошибка про взаимоисключающие флаги, получено %v", err)
	}
	// Явная форма --on=false равнозначна --off, а не «флаг не задан».
	cmd = userTestCmd(t, dir, dbPath)
	mustSet(t, cmd.Flags(), "on", "false")
	if err := runUserShowInList(cmd, []string{"klad"}); err != nil {
		t.Fatalf("--on=false должен приниматься как скрытие, получено %v", err)
	}

	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := auth.NewRepo(db)

	// --on: klad появляется в списке выбора.
	cmd = userTestCmd(t, dir, dbPath)
	mustSet(t, cmd.Flags(), "on", "true")
	if err := runUserShowInList(cmd, []string{"klad"}); err != nil {
		t.Fatalf("--on: %v", err)
	}
	sel, err := repo.ListForSelection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sel) != 1 || sel[0].Login != "klad" {
		t.Fatalf("после --on ожидался klad в списке выбора, получено %+v", sel)
	}

	// --off: klad исчезает из списка выбора.
	cmd = userTestCmd(t, dir, dbPath)
	mustSet(t, cmd.Flags(), "off", "true")
	if err := runUserShowInList(cmd, []string{"klad"}); err != nil {
		t.Fatalf("--off: %v", err)
	}
	sel, err = repo.ListForSelection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sel) != 0 {
		t.Fatalf("после --off список выбора должен быть пуст, получено %+v", sel)
	}

	// Неизвестный логин — ошибка с внятным текстом.
	cmd = userTestCmd(t, dir, dbPath)
	mustSet(t, cmd.Flags(), "on", "true")
	err = runUserShowInList(cmd, []string{"nope"})
	if err == nil || !strings.Contains(err.Error(), "не найден") {
		t.Fatalf("для несуществующего логина ожидалась ошибка «не найден», получено %v", err)
	}
}

func TestGeneratePassword(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		pw, err := generatePassword(16)
		if err != nil {
			t.Fatal(err)
		}
		if len(pw) != 16 {
			t.Fatalf("длина пароля %d, ожидалось 16", len(pw))
		}
		if seen[pw] {
			t.Fatalf("сгенерирован повторяющийся пароль %q", pw)
		}
		seen[pw] = true
		for _, c := range pw {
			if !containsRune(passwordAlphabet, c) {
				t.Fatalf("символ %q вне алфавита", c)
			}
		}
	}
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}

// Ошибка отзыва сессий при 2fa reset не проглатывается (issue #862): сброс
// делают в том числе потому, что фактор мог скомпрометировать не владелец, и
// «Второй фактор снят» при живых сессиях — ложное подтверждение. Тот же класс
// «сбой выдан за успех» #648 чинил в user passwd, но соседняя команда осталась.
func TestUserCLI_2FAReset_KickFailureIsReported(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "users.db")

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
	// Живая сессия обязательна: BEFORE DELETE-триггер стреляет по строкам, и на
	// пустой таблице отзыв «успешен» независимо от фикса.
	if _, err := repo.CreateSession(ctx, u.ID, auth.SessionMeta{}); err != nil {
		t.Fatalf("сессия: %v", err)
	}
	// Ломаем именно отзыв: триггер переживает EnsureSchema при повторном
	// открытии базы командой (в отличие от DROP TABLE, которую схема бы
	// пересоздала) и валит только DELETE из _sessions.
	if _, err := db.Exec(ctx, `CREATE TRIGGER _sessions_no_delete BEFORE DELETE ON _sessions
		BEGIN SELECT RAISE(ABORT, 'сессии заблокированы тестом'); END`); err != nil {
		t.Fatalf("триггер: %v", err)
	}
	db.Close()

	cmd = userTestCmd(t, dir, dbPath)
	err = runUser2FAReset(cmd, []string{"admin"})
	if err == nil {
		t.Fatal("ошибка отзыва сессий проглочена — команда отчиталась успехом")
	}
	if !strings.Contains(err.Error(), "отозвать не удалось") {
		t.Errorf("ошибка не говорит про отзыв сессий: %v", err)
	}

	// Первый факт тоже сообщён честно: фактор действительно снят.
	db, err = storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo = auth.NewRepo(db)
	if on, err := repo.TOTPEnabled(ctx, u.ID); err != nil || on {
		t.Fatalf("второй фактор не снят: on=%v err=%v", on, err)
	}
}

// #620: офлайн-снятие второго фактора — восстановление доступа без входа и без
// другого администратора (утрата устройства-аутентификатора; запертая база).
func TestUserCLI_2FAReset(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "users.db")

	cmd := userTestCmd(t, dir, dbPath)
	mustSet(t, cmd.Flags(), "admin", "true")
	mustSet(t, cmd.Flags(), "generate", "true")
	if err := runUserAdd(cmd, []string{"admin"}); err != nil {
		t.Fatalf("создание админа: %v", err)
	}

	// Привяжем второй фактор напрямую и убедимся, что он включён.
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
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
	if on, _ := repo.TOTPEnabled(ctx, u.ID); !on {
		t.Fatal("подготовка: второй фактор не включён")
	}
	db.Close()

	// Сброс через CLI.
	cmd = userTestCmd(t, dir, dbPath)
	if err := runUser2FAReset(cmd, []string{"admin"}); err != nil {
		t.Fatalf("user 2fa reset: %v", err)
	}

	db, err = storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo = auth.NewRepo(db)
	if on, err := repo.TOTPEnabled(ctx, u.ID); err != nil || on {
		t.Fatalf("второй фактор не снят: on=%v err=%v", on, err)
	}

	// Несуществующий логин — понятная ошибка, а не паника.
	cmd = userTestCmd(t, dir, dbPath)
	if err := runUser2FAReset(cmd, []string{"нетакого"}); err == nil {
		t.Fatal("сброс для несуществующего логина должен вернуть ошибку")
	}
}
