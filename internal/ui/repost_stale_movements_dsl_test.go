package ui

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
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
	mk := func(sub string) string {
		p := filepath.Join(dir, sub)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
		return p
	}
	regDir := mk("registers")
	docDir := mk("documents")
	procDir := mk("processors")
	srcDir := mk("src")

	reg := func(name string) []byte {
		return []byte("name: " + name + "\n" +
			"dimensions:\n  - {name: Т, type: string}\n" +
			"resources:\n  - {name: К, type: number}\n")
	}
	if err := os.WriteFile(filepath.Join(regDir, "рега.yaml"), reg("РегА"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(regDir, "регб.yaml"), append(reg("РегБ"), []byte("totals:\n  enabled: true\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mk("inforegs"), "инфов.yaml"), reg("ИнфоВ"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mk("accounts"), "основной.yaml"), []byte(`name: Основной
accounts:
  - {code: "41", name: Товары, kind: active}
  - {code: "60", name: Поставщики, kind: passive}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mk("accountregs"), "бухг.yaml"), []byte(`name: БухГ
accounts: Основной
resources:
  - {name: Сумма, type: number}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docDir, "док.yaml"), []byte("name: Док\nposting: true\nfields:\n"+
		"  - {name: Дата, type: date}\n  - {name: Флаг, type: bool}\n  - {name: Ключ, type: string}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "Док.posting.os"), []byte(`Процедура ОбработкаПроведения()
  Движения.РегА.Очистить();
  Дв = Движения.РегА.Добавить();
  Дв.Т = this.Ключ;
  Дв.К = 1;
  Если this.Флаг Тогда
    Дв2 = Движения.РегБ.Добавить();
    Дв2.Т = this.Ключ;
    Дв2.К = 100;
    Дв3 = Движения.ИнфоВ.Добавить();
    Дв3.Т = this.Ключ;
    Дв3.К = 5;
    Дв4 = Движения.БухГ.Добавить();
    Дв4.СчётДт = "41";
    Дв4.СчётКт = "60";
    Дв4.Сумма = 100;
  КонецЕсли;
КонецПроцедуры
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "проба.yaml"), []byte("name: Проба\ntitle: Проба\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "Проба.proc.os"), []byte(`Процедура Выполнить()
  Другой = Документы.Док.Создать();
  Другой.Дата = ТекущаяДата();
  Другой.Ключ = "k2";
  Другой.Флаг = Истина;
  Другой.Провести();
  Д = Документы.Док.Создать();
  Д.Дата = ТекущаяДата();
  Д.Ключ = "k1";
  Д.Флаг = Истина;
  Д.Провести();
  Д.Флаг = Ложь;
  Д.Провести();
КонецПроцедуры
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		if err := db.Migrate(ctx, proj.Entities); err != nil {
			t.Fatalf("миграция сущностей: %v", err)
		}
		if err := db.MigrateRegisters(ctx, proj.Registers); err != nil {
			t.Fatalf("миграция регистров: %v", err)
		}
		if err := db.MigrateInfoRegisters(ctx, proj.InfoRegisters); err != nil {
			t.Fatal(err)
		}
		if err := db.EnsureAccountsTable(ctx); err != nil {
			t.Fatal(err)
		}
		if err := db.SyncAccounts(ctx, proj.ChartsOfAccounts); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateAccountRegisters(ctx, proj.AccountRegisters); err != nil {
			t.Fatal(err)
		}

		msgs, runErr, err := RunProcessorOffline(ctx, proj, db, "Проба", nil, nil)
		if err != nil {
			t.Fatalf("RunProcessorOffline: %v", err)
		}
		if runErr != nil {
			t.Fatalf("обработка упала: %v (сообщения %v)", runErr, msgs)
		}

		count := func(table, recorder, key string) int {
			t.Helper()
			var n int
			if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+table+" WHERE "+recorder+" = (SELECT id FROM док WHERE ключ = $1)", key).Scan(&n); err != nil {
				t.Fatalf("%s: %v", table, err)
			}
			return n
		}
		if n := count("рег_рега", "recorder", "k1"); n != 1 {
			t.Errorf("РегА: %d движений, ждали 1 (модуль пишет его всегда)", n)
		}
		for _, reg := range []struct{ table, recorder string }{
			{"рег_регб", "recorder"}, {"инфо_инфов", "recorder"}, {"акк_бухг", "регистратор"},
		} {
			if n := count(reg.table, reg.recorder, "k1"); n != 0 {
				t.Errorf("%s: после перепроведения с Флаг = Ложь осталось %d движений", reg.table, n)
			}
			if n := count(reg.table, reg.recorder, "k2"); n != 1 {
				t.Errorf("%s: у другого документа %d движений, ждали 1", reg.table, n)
			}
		}
		for key, want := range map[string]float64{"k1": 0, "k2": 100} {
			var raw string
			if err := db.QueryRow(ctx, "SELECT CAST(COALESCE(SUM(к), 0) AS TEXT) FROM "+metadata.RegisterTotalsTableName("РегБ")+" WHERE т = $1", key).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			got, err := strconv.ParseFloat(raw, 64)
			if err != nil || got != want {
				t.Errorf("итоги по %s: %s, err=%v, ждали %v", key, raw, err, want)
			}
		}
	})
}
