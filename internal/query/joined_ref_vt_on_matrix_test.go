package query_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
)

// ПО следующего JOIN с VT принадлежит VT, а не прежнему присоединённому
// справочнику (#1459, девятое ревью). Проверяем строки через Compile → Run.
func TestJoinedRefFollowingVirtualTableOnMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := joinedRefEntities()
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatal(err)
		}
		seedJoinedRef(t, ctx, db, ents)
		reg := &metadata.Register{Name: "ОстаткиПробы", Dimensions: []metadata.Field{{Name: "Код", Type: metadata.FieldTypeString}}, Resources: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}}}
		ir := &metadata.InfoRegister{Name: "СведенияПробы", Periodic: true, Dimensions: []metadata.Field{{Name: "Код", Type: metadata.FieldTypeString}}, Resources: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}}}
		ar := &metadata.AccountRegister{Name: "БухПробы", Accounts: "ПланПробы", Resources: []metadata.Field{{Name: "Сумма", Type: metadata.FieldTypeNumber}}}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{ar}); err != nil {
			t.Fatal(err)
		}
		if err := db.EnsureAccountsTable(ctx); err != nil {
			t.Fatal(err)
		}
		opts := query.CompileOpts{Entities: ents, Registers: []*metadata.Register{reg}, InfoRegs: []*metadata.InfoRegister{ir}, AccountRegs: []*metadata.AccountRegister{ar}, Dialect: db.Dialect()}
		const joined = ` ИЗ Справочник.Базы КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.Код = Б.Код`
		const nav = `П.ГлавныйУзел.Наименование`
		variants := []struct{ name, source, alias, key, value string }{
			{"accum-balances", `РегистрНакопления.ОстаткиПробы.Остатки()`, "остатки_остаткипробы", "Код", "КоличествоОстаток"},
			{"accum-turnovers", `РегистрНакопления.ОстаткиПробы.Обороты()`, "обороты_остаткипробы", "Код", "КоличествоОборот"},
			{"info-last", `РегистрСведений.СведенияПробы.СрезПоследних()`, "срезпоследних_сведенияпробы", "Код", "Количество"},
			{"info-first", `РегистрСведений.СведенияПробы.СрезПервых()`, "срезпервых_сведенияпробы", "Код", "Количество"},
			{"account-balances", `РегистрБухгалтерии.БухПробы.Остатки()`, "остатки_бухпробы", "Счёт", "СуммаОстаток"},
			{"account-turnovers", `РегистрБухгалтерии.БухПробы.Обороты()`, "обороты_бухпробы", "Счёт", "Сумма_Дт"},
		}
		run := func(t *testing.T, text string) []map[string]any {
			t.Helper()
			r, err := query.Compile(text, opts)
			if err != nil {
				t.Fatalf("Compile: %v\nQuery: %s", err, text)
			}
			rows, _, err := query.Run(ctx, db, &r)
			if err != nil {
				t.Fatalf("Run: %v\nSQL: %s", err, r.SQL)
			}
			return rows
		}
		check := func(t *testing.T, text string, count int, value any) {
			t.Helper()
			rows := run(t, text)
			if len(rows) != count {
				t.Fatalf("rows=%v, want %d\nQuery: %s", rows, count, text)
			}
			for _, row := range rows {
				if row["имя"] != "План-1" {
					t.Fatalf("unexpected plan: %v", row)
				}
				got, present := row["значение"]
				if strings.Contains(text, "КАК Значение") && !present {
					t.Fatalf("missing projected value: %v", row)
				}
				if present && (value == nil && got != nil || value != nil && fmt.Sprint(got) != fmt.Sprint(value)) {
					t.Fatalf("value=%v, want %v; rows=%v", got, value, rows)
				}
			}
		}
		// Исходная фикстура ревью: движений нет, LEFT JOIN всё равно возвращает план.
		for _, v := range variants {
			for _, explicit := range []bool{false, true} {
				alias, source := v.alias, v.source
				if explicit {
					alias, source = "Р", source+` КАК Р`
				}
				t.Run(v.name+fmt.Sprintf("/empty/explicit=%t", explicit), func(t *testing.T) {
					text := `ВЫБРАТЬ П.Наименование КАК Имя` + joined + ` ЛЕВОЕ СОЕДИНЕНИЕ ` + source + ` ПО ` + alias + `.` + v.key + ` = Б.Код И ` + nav + ` = Б.Наименование`
					check(t, text, 1, nil)
				})
			}
		}
		at := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
		if err := db.WriteMovements(ctx, reg.Name, "Док", uuid.New(), []map[string]any{{"ВидДвижения": "Приход", "Код": "GL", "Количество": 5}}, reg, &at); err != nil {
			t.Fatal(err)
		}
		if err := db.InfoRegSet(ctx, ir, map[string]any{"Код": "GL"}, map[string]any{"Количество": 5}, &at); err != nil {
			t.Fatal(err)
		}
		if err := db.SyncAccounts(ctx, []*metadata.ChartOfAccounts{{Name: ar.Accounts, Accounts: []metadata.Account{{Code: "GL", Name: "Главная", Kind: "active"}, {Code: "OT", Name: "Другая", Kind: "passive"}}}}); err != nil {
			t.Fatal(err)
		}
		if err := db.WriteAccountMovements(ctx, ar.Name, "Док", uuid.New(), []map[string]any{{"счётдт": "GL", "счёткт": "OT", "сумма": decimal.NewFromInt(5)}}, ar, &at); err != nil {
			t.Fatal(err)
		}
		for _, v := range variants {
			for _, explicit := range []bool{false, true} {
				alias, source := v.alias, v.source
				if explicit {
					alias, source = "Р", source+` КАК Р`
				}
				t.Run(v.name+fmt.Sprintf("/seeded/explicit=%t", explicit), func(t *testing.T) {
					projection := `ВЫБРАТЬ П.Наименование КАК Имя, ` + alias + `.` + v.value + ` КАК Значение`
					for _, c := range []struct {
						name, join, condition string
						count                 int
						value                 any
					}{
						{"left-match", "ЛЕВОЕ", `Б.Наименование`, 1, "5"},
						{"left-mismatch", "ЛЕВОЕ", `"Другая"`, 1, nil},
						{"inner-match", "ВНУТРЕННЕЕ", `Б.Наименование`, 1, "5"},
						{"inner-mismatch", "ВНУТРЕННЕЕ", `"Другая"`, 0, nil},
					} {
						t.Run(c.name, func(t *testing.T) {
							text := projection + joined + ` ` + c.join + ` СОЕДИНЕНИЕ ` + source + ` ПО ` + alias + `.` + v.key + ` = Б.Код И ` + nav + ` = ` + c.condition
							check(t, text, c.count, c.value)
						})
					}
					from := joined + ` ЛЕВОЕ СОЕДИНЕНИЕ ` + source + ` ПО ` + alias + `.` + v.key + ` = Б.Код И ` + nav + ` = Б.Наименование`
					for _, c := range []struct {
						name, text string
						count      int
					}{
						{"nested-select", `ВЫБРАТЬ Д.Имя, Д.Значение ИЗ (` + projection + from + `) КАК Д`, 1},
						{"union-sibling", projection + from + ` ОБЪЕДИНИТЬ ВСЕ ` + projection + from, 2},
						{"after-on-subquery", projection + joined + ` ЛЕВОЕ СОЕДИНЕНИЕ ` + source + ` ПО ` + alias + `.` + v.key + ` В (ВЫБРАТЬ Б2.Код ИЗ Справочник.Базы КАК Б2) И ` + nav + ` = Б.Наименование`, 1},
						{"correlated-on", projection + joined + ` ЛЕВОЕ СОЕДИНЕНИЕ ` + source + ` ПО ` + alias + `.` + v.key + ` В (ВЫБРАТЬ Б2.Код ИЗ Справочник.Базы КАК Б2 ГДЕ Б2.Наименование = ` + nav + `)`, 1},
						{"following-ordinary-join", projection + from + ` ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Базы КАК Б3 ПО Б3.Наименование = ` + nav, 1},
						{"following-derived-join", projection + from + ` ЛЕВОЕ СОЕДИНЕНИЕ (ВЫБРАТЬ Наименование ИЗ Справочник.Базы) КАК Б3 ПО Б3.Наименование = ` + nav, 1},
						{"navigation-in-select", `ВЫБРАТЬ П.Наименование КАК Имя, ` + nav + ` КАК Узел` + joined + ` ЛЕВОЕ СОЕДИНЕНИЕ ` + source + ` ПО ` + alias + `.` + v.key + ` = Б.Код`, 1},
						{"navigation-in-where", projection + joined + ` ЛЕВОЕ СОЕДИНЕНИЕ ` + source + ` ПО ` + alias + `.` + v.key + ` = Б.Код ГДЕ ` + nav + ` = "Главная"`, 1},
					} {
						t.Run(c.name, func(t *testing.T) {
							check(t, c.text, c.count, "5")
							if c.name == "navigation-in-select" && run(t, c.text)[0]["узел"] != "Главная" {
								t.Fatal("wrong reference target")
							}
						})
					}
					// Вложенная VT не меняет владельца собственного ПО внешнего П.
					negative := `ВЫБРАТЬ П.Наименование КАК Имя ИЗ Справочник.Базы КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.Код В (ВЫБРАТЬ ` + alias + `.` + v.key + ` ИЗ ` + source + `) И ` + nav + ` = Б.Наименование`
					r, err := query.Compile(negative, opts)
					if err == nil || !strings.Contains(err.Error(), "внутри ПО не поддерживается") {
						t.Fatalf("own ON must fail explicitly: %v\nSQL: %s", err, r.SQL)
					}
				})
			}
		}
	})
}
