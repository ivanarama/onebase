package dbcheck

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Движения проводимого документа со снятым признаком проведения — расхождение,
// которое проверка сирот не видит: регистратор жив. Так его оставляла загрузка
// пакета обмена (приёмник ставил «не проведён», движения прежнего
// перепроведения оставались). Проверка обязана найти такие движения в обоих
// семействах — регистре накопления и регистре бухгалтерии, — а исправление
// удалить только их и пересчитать итоги.
func TestUnpostedMovementsCheckMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		suffix := uuid.NewString()[:8]
		doc := &metadata.Entity{
			Name: "Продажа" + suffix, Kind: metadata.KindDocument, Posting: true,
			Fields: []metadata.Field{{Name: "Сумма", Type: metadata.FieldTypeNumber, Length: 15, Scale: 2}},
		}
		// Непроводимый документ с движениями — не предмет этой проверки: признака
		// проведения у него нет по замыслу.
		note := &metadata.Entity{
			Name: "Заметка" + suffix, Kind: metadata.KindDocument,
			Fields: []metadata.Field{{Name: "Сумма", Type: metadata.FieldTypeNumber, Length: 15, Scale: 2}},
		}
		reg := &metadata.Register{
			Name:       "Остатки" + suffix,
			Dimensions: []metadata.Field{{Name: "Товар", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Сумма", Type: metadata.FieldTypeNumber, Length: 15, Scale: 2}},
			Totals:     metadata.RegisterTotals{Enabled: true},
		}
		ar := &metadata.AccountRegister{
			Name:      "Бух" + suffix,
			Accounts:  "План" + suffix,
			Resources: []metadata.Field{{Name: "Сумма", Type: metadata.FieldTypeNumber, Length: 15, Scale: 2}},
			Totals:    metadata.RegisterTotals{Enabled: true},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{doc, note}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatalf("MigrateRegisters: %v", err)
		}
		if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{ar}); err != nil {
			t.Fatalf("MigrateAccountRegisters: %v", err)
		}
		if err := db.EnsureAccountsTable(ctx); err != nil {
			t.Fatal(err)
		}
		if err := db.SyncAccounts(ctx, []*metadata.ChartOfAccounts{{Name: ar.Accounts, Accounts: []metadata.Account{
			{Code: "50", Name: "Касса", Kind: "active"}, {Code: "51", Name: "Счёт", Kind: "active"},
		}}}); err != nil {
			t.Fatal(err)
		}

		period := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
		write := func(ent *metadata.Entity, sum int, posted bool) uuid.UUID {
			t.Helper()
			id := uuid.New()
			if err := db.Upsert(ctx, ent.Name, id, map[string]any{"Сумма": sum}, ent); err != nil {
				t.Fatalf("Upsert %s: %v", ent.Name, err)
			}
			if err := db.WriteMovements(ctx, reg.Name, ent.Name, id, []map[string]any{
				{"Товар": "Гвоздь", "Сумма": sum, "ВидДвижения": "Приход"},
			}, reg, &period); err != nil {
				t.Fatalf("WriteMovements: %v", err)
			}
			if err := db.WriteAccountMovements(ctx, ar.Name, ent.Name, id, []map[string]any{
				{"счётдт": "50", "счёткт": "51", "Сумма": sum},
			}, ar, &period); err != nil {
				t.Fatalf("WriteAccountMovements: %v", err)
			}
			if posted {
				if err := db.SetPosted(ctx, ent.Name, id, true); err != nil {
					t.Fatal(err)
				}
			}
			return id
		}
		write(doc, 100, true) // проведён — движения законны
		write(doc, 40, false) // не проведён — движения лишние
		write(note, 7, false) // непроводимый документ — не трогаем

		env := &Env{
			DB: db, Entities: []*metadata.Entity{doc, note},
			Registers: []*metadata.Register{reg}, AccountRegisters: []*metadata.AccountRegister{ar},
		}
		res := findResult(t, Run(ctx, env, []Check{unpostedMovementsCheck{}}, nil), "unposted-movements")
		if res.Severity != SeverityError || len(res.Findings) != 2 {
			t.Fatalf("ожидались две находки (регистр накопления и бухрегистр): %+v", res)
		}
		for _, f := range res.Findings {
			if f.Count != 1 {
				t.Errorf("%s: найдено %d движений, ожидалось 1 (только непроведённого документа)", f.Object, f.Count)
			}
		}

		fixed := findResult(t, Run(ctx, env, []Check{unpostedMovementsCheck{}},
			map[string]bool{"unposted-movements": true}), "unposted-movements")
		if fixed.Error != "" {
			t.Fatalf("исправление упало: %s", fixed.Error)
		}
		sum := func(table string, res metadata.Field) string {
			t.Helper()
			d, err := sumExpr(ctx, env, table, "SUM("+metadata.ColumnName(res)+")")
			if err != nil {
				t.Fatal(err)
			}
			return d.String()
		}
		// Остались движения проведённого (100) и непроводимого (7) документов.
		if got := sum(metadata.RegisterTableName(reg.Name), reg.Resources[0]); got != "107" {
			t.Errorf("сумма движений регистра после исправления = %s, ожидалось 107", got)
		}
		if got := sum(metadata.AccountRegTableName(ar.Name), ar.Resources[0]); got != "107" {
			t.Errorf("сумма проводок после исправления = %s, ожидалось 107", got)
		}
		// Итоги пересчитаны: проверка итогов после исправления чиста.
		for _, name := range []string{"totals", "account-totals"} {
			var check Check
			for _, c := range All() {
				if c.Name() == name {
					check = c
				}
			}
			if r := findResult(t, Run(ctx, env, []Check{check}, nil), name); r.Severity != SeverityOK {
				t.Errorf("после исправления проверка %s не чиста: %+v", name, r)
			}
		}
		if again := findResult(t, Run(ctx, env, []Check{unpostedMovementsCheck{}}, nil), "unposted-movements"); again.Severity != SeverityOK {
			t.Errorf("после исправления проверка снова находит: %+v", again)
		}
	})
}
