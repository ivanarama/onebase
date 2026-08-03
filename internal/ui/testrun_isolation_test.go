package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// writeIsolationProject создаёт проект со справочником Контрагент и одним
// пишущим тестом: он создаёт запись и проверяет, что она читается в пределах
// теста. Изоляция проверяется снаружи — по числу строк в таблице после прогона.
func writeIsolationProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	catDir := filepath.Join(dir, "catalogs")
	procDir := filepath.Join(dir, "processors")
	srcDir := filepath.Join(dir, "src")
	for _, d := range []string{catDir, procDir, srcDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	must := func(p, content string) {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil { //nolint:gosec // G703: путь построен под контролем (SafeJoin либо каталог, заданный администратором)
			t.Fatal(err)
		}
	}
	must(filepath.Join(catDir, "контрагент.yaml"),
		"name: Контрагент\nfields:\n  - name: Наименование\n    type: string\n")
	must(filepath.Join(procDir, "тестзапись.yaml"), "name: ТестЗапись\nkind: test\n")
	must(filepath.Join(srcDir, "тестзапись.proc.os"),
		"Процедура Выполнить()\n"+
			"  К = Справочники.Контрагент.Создать();\n"+
			"  К.Наименование = \"Ромашка\";\n"+
			"  Ссылка = К.Записать();\n"+
			"  Утверждать.Заполнено(Ссылка, \"ссылка после записи\");\n"+
			"  Об = Ссылка.ПолучитьОбъект();\n"+
			"  Утверждать.Равно(Об.Наименование, \"Ромашка\", \"чтение своей записи в транзакции\");\n"+
			"КонецПроцедуры\n")
	return dir
}

func loadIsolationProject(t *testing.T) (*project.Project, *storage.DB) {
	t.Helper()
	dir := writeIsolationProject(t)
	ctx := context.Background()
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Cleanup(func() { proj.Close() })
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "iso.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return proj, db
}

func countCounterparties(t *testing.T, db *storage.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), "SELECT COUNT(*) FROM контрагент").Scan(&n); err != nil {
		t.Fatalf("count контрагент: %v", err)
	}
	return n
}

// Под transaction-изоляцией пишущий тест сам себя видит (проверки проходят), но
// после прогона запись откатана — в таблице 0 строк.
func TestRunTests_TransactionRollsBack(t *testing.T) {
	proj, db := loadIsolationProject(t)
	res, err := RunTests(context.Background(), proj, db, TestRunOptions{Isolation: IsolationTransaction})
	if err != nil {
		t.Fatalf("RunTests: %v", err)
	}
	if !res.OK() {
		t.Fatalf("пишущий тест должен пройти (запись видна в своей транзакции): %+v", res.Cases)
	}
	if n := countCounterparties(t, db); n != 0 {
		t.Fatalf("после transaction-изоляции таблица должна быть пуста, строк: %d", n)
	}
}

// Под none-изоляцией запись персистится — после прогона 1 строка.
func TestRunTests_NoIsolationPersists(t *testing.T) {
	proj, db := loadIsolationProject(t)
	res, err := RunTests(context.Background(), proj, db, TestRunOptions{Isolation: IsolationNone})
	if err != nil {
		t.Fatalf("RunTests: %v", err)
	}
	if !res.OK() {
		t.Fatalf("пишущий тест должен пройти: %+v", res.Cases)
	}
	if n := countCounterparties(t, db); n != 1 {
		t.Fatalf("без изоляции запись должна остаться, строк: %d (ожидалась 1)", n)
	}
}
