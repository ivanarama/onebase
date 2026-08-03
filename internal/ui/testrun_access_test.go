package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// writeAccessProject создаёт проект с сущностями, ролью Оператор (field_access +
// row_access) и тест-обработкой, проверяющей полевой и строковый доступ.
func writeAccessProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { //nolint:gosec // G703: путь построен под контролем (SafeJoin либо каталог, заданный администратором)
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil { //nolint:gosec // G703: путь построен под контролем (SafeJoin либо каталог, заданный администратором)
			t.Fatal(err)
		}
	}

	write("catalogs/Клиент.yaml",
		"name: Клиент\nfields:\n  - name: Наименование\n    type: string\n  - name: Телефон\n    type: string\n")
	write("documents/Задача.yaml",
		"name: Задача\nfields:\n  - name: Адресат\n    type: string\n  - name: Тема\n    type: string\n")

	write("roles/Оператор.yaml", ""+
		"name: Оператор\n"+
		"permissions:\n"+
		"  catalogs:\n"+
		"    Клиент: [read]\n"+
		"  documents:\n"+
		"    Задача: [read, write]\n"+
		"  field_access:\n"+
		"    catalogs:\n"+
		"      Клиент:\n"+
		"        Телефон: { read: mask_tail, keep: 4 }\n"+
		"  row_access:\n"+
		"    documents:\n"+
		"      Задача:\n"+
		"        read:\n"+
		"          field: Адресат\n"+
		"          op: eq\n"+
		"          value: { user: login }\n")

	name := "ТестДоступа"
	write("processors/"+name+".yaml", "name: "+name+"\nkind: test\n")
	write("src/"+strings.ToLower(name)+".proc.os", "Процедура Выполнить()\n"+
		"  Утверждать.ПолеМаскируется(\"Оператор\", \"справочник\", \"Клиент\", \"Телефон\", \"телефон под маской\");\n"+
		"  Утверждать.ПолеВидно(\"Оператор\", \"справочник\", \"Клиент\", \"Наименование\", \"имя открыто\");\n"+
		"  Утверждать.МаскаПоля(\"Оператор\", \"справочник\", \"Клиент\", \"Телефон\", \"1234567890\", \"••••••7890\", \"keep=4\");\n"+
		"  Утверждать.СтрокиОграничены(\"Оператор\", \"документ\", \"Задача\", \"читать\", \"видит только свои задачи\");\n"+
		"  Утверждать.СтрокиНеОграничены(\"Оператор\", \"справочник\", \"Клиент\", \"читать\", \"клиентов видит всех\");\n"+
		"КонецПроцедуры\n")

	return dir
}

func TestRunTests_AccessAsserts(t *testing.T) {
	dir := writeAccessProject(t)
	ctx := context.Background()
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Cleanup(func() { proj.Close() })
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "access.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	res, err := RunTests(ctx, proj, db, TestRunOptions{})
	if err != nil {
		t.Fatalf("RunTests: %v", err)
	}
	c := caseByName(res, "ТестДоступа")
	if c == nil {
		t.Fatal("ТестДоступа не выполнен")
	}
	if c.Err != nil {
		t.Fatalf("ТестДоступа: ошибка выполнения: %v", c.Err)
	}
	if c.Passed != 5 || c.Failed != 0 {
		t.Fatalf("ожидалось 5 прошло / 0 провалено, получено %d / %d (%+v)", c.Passed, c.Failed, c.Asserts)
	}
}
