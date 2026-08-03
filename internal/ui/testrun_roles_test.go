package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// writeRoleAssertProject создаёт проект с ролью в roles/*.yaml и двумя тест-
// обработками: одна с верными ожиданиями матрицы прав (все проверки проходят),
// другая с заведомо ложными (все проваливаются, включая ошибки резолва).
func writeRoleAssertProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	procDir := filepath.Join(dir, "processors")
	srcDir := filepath.Join(dir, "src")
	rolesDir := filepath.Join(dir, "roles")
	for _, d := range []string{procDir, srcDir, rolesDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	role := "" +
		"name: Кладовщик\n" +
		"description: тест матрицы прав\n" +
		"permissions:\n" +
		"  documents:\n" +
		"    Реализация: [read, write, post, unpost]\n" +
		"  catalogs:\n" +
		"    Организация: [read]\n"
	if err := os.WriteFile(filepath.Join(rolesDir, "warehouse.yaml"), []byte(role), 0o644); err != nil {
		t.Fatal(err)
	}

	add := func(name, code string) {
		yaml := "name: " + name + "\nkind: test\n"
		if err := os.WriteFile(filepath.Join(procDir, name+".yaml"), []byte(yaml), 0o644); err != nil { //nolint:gosec // G703: путь построен под контролем (SafeJoin либо каталог, заданный администратором)
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, name+".proc.os"), []byte(code), 0o644); err != nil { //nolint:gosec // G703: путь построен под контролем (SafeJoin либо каталог, заданный администратором)
			t.Fatal(err)
		}
	}

	// Все 4 проверки должны пройти: синонимы операций (провести→post,
	// изменять→write, читать→read) и видов (документ/справочник) нормализуются.
	add("RoleOK", "Процедура Выполнить()\n"+
		"  Утверждать.РольМожет(\"Кладовщик\", \"документ\", \"Реализация\", \"провести\", \"проводит реализацию\");\n"+
		"  Утверждать.РольНеМожет(\"Кладовщик\", \"документ\", \"Реализация\", \"удалять\", \"не удаляет\");\n"+
		"  Утверждать.РольНеМожет(\"Кладовщик\", \"справочник\", \"Организация\", \"изменять\", \"не редактирует орг\");\n"+
		"  Утверждать.РольМожет(\"Кладовщик\", \"справочник\", \"Организация\", \"читать\", \"читает орг\");\n"+
		"КонецПроцедуры\n")

	// Все 3 проверки должны провалиться: неверное ожидание, неизвестная роль,
	// неизвестный вид (последние два — провал с сообщением об ошибке резолва).
	add("RoleBad", "Процедура Выполнить()\n"+
		"  Утверждать.РольМожет(\"Кладовщик\", \"документ\", \"Реализация\", \"удалять\", \"delete не выдан\");\n"+
		"  Утверждать.РольМожет(\"НетТакойРоли\", \"документ\", \"Реализация\", \"читать\", \"неизвестная роль\");\n"+
		"  Утверждать.РольМожет(\"Кладовщик\", \"чтотоневерное\", \"Реализация\", \"читать\", \"неизвестный вид\");\n"+
		"КонецПроцедуры\n")

	return dir
}

func TestRunTests_RoleAsserts(t *testing.T) {
	dir := writeRoleAssertProject(t)
	ctx := context.Background()
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Cleanup(func() { proj.Close() })
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "roles.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	res, err := RunTests(ctx, proj, db, TestRunOptions{})
	if err != nil {
		t.Fatalf("RunTests: %v", err)
	}

	ok := caseByName(res, "RoleOK")
	if ok == nil {
		t.Fatal("RoleOK не выполнен")
	}
	if ok.Err != nil {
		t.Fatalf("RoleOK: неожиданная ошибка выполнения: %v", ok.Err)
	}
	if ok.Passed != 4 || ok.Failed != 0 {
		t.Fatalf("RoleOK: ожидалось 4 прошло / 0 провалено, получено %d / %d (%+v)", ok.Passed, ok.Failed, ok.Asserts)
	}

	bad := caseByName(res, "RoleBad")
	if bad == nil {
		t.Fatal("RoleBad не выполнен")
	}
	if bad.Failed != 3 || bad.Passed != 0 {
		t.Fatalf("RoleBad: ожидалось 0 прошло / 3 провалено, получено %d / %d (%+v)", bad.Passed, bad.Failed, bad.Asserts)
	}
}
