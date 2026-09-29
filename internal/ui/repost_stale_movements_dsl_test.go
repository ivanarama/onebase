package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// Провести() из модуля снимает прежние движения из регистров, которых модуль
// проведения в этот раз не коснулся, — так же, как проведение формой и REST.
//
// DSL-путь записи документа держал собственную копию записи движений и писал
// только регистры, где модуль вызвал Добавить() или Очистить(). Сценарий —
// ровно то, как дефект виден пользователю в procrun: документ пишет РегБ
// только при Флаг = Истина; после перепроведения со снятым флагом движение в
// РегБ оставалось.
func TestDSLPost_ClearsMovementsOfUntouchedRegisters(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg := func(name string) string {
		return "name: " + name + "\n" +
			"dimensions:\n  - {name: Т, type: string}\n" +
			"resources:\n  - {name: К, type: number}\n"
	}
	write("registers/рега.yaml", reg("РегА"))
	write("registers/регб.yaml", reg("РегБ"))
	write("documents/док.yaml", "name: Док\nposting: true\nfields:\n"+
		"  - {name: Дата, type: date}\n  - {name: Флаг, type: bool}\n")
	write("src/Док.posting.os", `Процедура ОбработкаПроведения()
  Движения.РегА.Очистить();
  Дв = Движения.РегА.Добавить();
  Дв.Т = "x";
  Дв.К = 1;
  Если this.Флаг Тогда
    Дв2 = Движения.РегБ.Добавить();
    Дв2.Т = "x";
    Дв2.К = 100;
  КонецЕсли;
КонецПроцедуры
`)
	write("processors/проба.yaml", "name: Проба\ntitle: Проба\n")
	write("src/Проба.proc.os", `Процедура Выполнить()
  Д = Документы.Док.Создать();
  Д.Дата = ТекущаяДата();
  Д.Флаг = Истина;
  Д.Провести();
  Д.Флаг = Ложь;
  Д.Провести();
КонецПроцедуры
`)

	ctx := context.Background()
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatalf("миграция сущностей: %v", err)
	}
	if err := db.MigrateRegisters(ctx, proj.Registers); err != nil {
		t.Fatalf("миграция регистров: %v", err)
	}

	msgs, runErr, err := RunProcessorOffline(ctx, proj, db, "Проба", nil, nil)
	if err != nil {
		t.Fatalf("RunProcessorOffline: %v", err)
	}
	if runErr != nil {
		t.Fatalf("обработка упала: %v (сообщения %v)", runErr, msgs)
	}

	count := func(table string) int {
		t.Helper()
		var n int
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		return n
	}
	if n := count("рег_рега"); n != 1 {
		t.Errorf("РегА: %d движений, ждали 1 (модуль пишет его всегда)", n)
	}
	if n := count("рег_регб"); n != 0 {
		t.Errorf("РегБ: после перепроведения с Флаг = Ложь осталось %d движений — модуль их больше не формирует", n)
	}
}
