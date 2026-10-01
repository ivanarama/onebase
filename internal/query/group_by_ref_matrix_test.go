package query_test

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Группировка по ссылочному полю шла по представлению: два разных склада с
// одинаковым наименованием «Основной» складывались в одну строку с общей суммой.
// Так ведут себя отчёты любой конфигурации, где повторяются имена контрагентов,
// складов или номенклатуры, и ошибку не видно — сумма сходится, строк меньше.
// Группировать нужно по ссылке; представление остаётся колонкой вывода.
//
// Одна и та же группировка проверяется для трёх источников с разной
// компиляцией ссылки: физическая таблица регистра, виртуальная таблица
// остатков и табличный документ. Прогон на обеих СУБД: PostgreSQL строже к
// списку GROUP BY и отверг бы вывод представления без него.
func TestGroupByReferenceKeepsSameNamedObjectsApartMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
		warehouse := &metadata.Entity{
			Name: "СкладГр" + suffix, Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		}
		refType := metadata.FieldType("reference:" + warehouse.Name)
		// JOIN приносит вторую склад_id: GROUP BY должен брать ID источника,
		// даже когда SELECT уже создал выходной алиас склад.
		warehouse.Fields = append(warehouse.Fields, metadata.Field{
			Name: "Склад", Type: refType, RefEntity: warehouse.Name,
		})
		doc := &metadata.Entity{
			Name: "ПриходГр" + suffix, Kind: metadata.KindDocument,
			Fields: []metadata.Field{
				{Name: "Склад", Type: refType, RefEntity: warehouse.Name},
				{Name: "Количество", Type: metadata.FieldTypeNumber},
			},
		}
		reg := &metadata.Register{
			Name:       "ОстаткиГр" + suffix,
			Dimensions: []metadata.Field{{Name: "Склад", Type: refType, RefEntity: warehouse.Name}},
			Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
		}
		entities := []*metadata.Entity{warehouse, doc}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatalf("миграция регистра: %v", err)
		}

		first, second := uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{first, second} {
			if err := db.Upsert(ctx, warehouse.Name, id, map[string]any{"Наименование": "Основной"}, warehouse); err != nil {
				t.Fatalf("склад: %v", err)
			}
		}
		period := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
		for _, in := range []struct {
			warehouse uuid.UUID
			qty       int
		}{{first, 10}, {second, 3}} {
			recorder := uuid.New()
			if err := db.Upsert(ctx, doc.Name, recorder, map[string]any{
				"Склад": in.warehouse.String(), "Количество": in.qty,
			}, doc); err != nil {
				t.Fatalf("документ: %v", err)
			}
			if err := db.WriteMovements(ctx, reg.Name, doc.Name, recorder, []map[string]any{
				{"Склад": in.warehouse.String(), "Количество": in.qty, "ВидДвижения": "Приход"},
			}, reg, &period); err != nil {
				t.Fatalf("движения: %v", err)
			}
		}

		opts := query.CompileOpts{Entities: entities, Registers: []*metadata.Register{reg}, Dialect: db.Dialect()}
		run := func(t *testing.T, src string) []string {
			t.Helper()
			compiled, err := query.Compile(src, opts)
			if err != nil {
				t.Fatalf("компиляция: %v\n%s", err, src)
			}
			rows, err := db.Query(ctx, compiled.SQL, compiled.Args...)
			if err != nil {
				t.Fatalf("исполнение: %v\nSQL: %s", err, compiled.SQL)
			}
			defer rows.Close()
			var got []string
			for rows.Next() {
				var name, qty string
				if err := rows.Scan(&name, &qty); err != nil {
					t.Fatalf("скан: %v", err)
				}
				got = append(got, name+"="+strings.TrimSuffix(strings.TrimSuffix(qty, ".0"), ".00"))
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			sort.Strings(got)
			return got
		}
		want := "[Основной=10 Основной=3]"
		for _, tc := range []struct{ name, src string }{
			{"регистр", `ВЫБРАТЬ Склад, СУММА(Количество) КАК К
				ИЗ РегистрНакопления.` + reg.Name + ` СГРУППИРОВАТЬ ПО Склад`},
			{"остатки", `ВЫБРАТЬ Склад, СУММА(КоличествоОстаток) КАК К
				ИЗ РегистрНакопления.` + reg.Name + `.Остатки() СГРУППИРОВАТЬ ПО Склад`},
			{"документ", `ВЫБРАТЬ Склад, СУММА(Количество) КАК К
				ИЗ Документ.` + doc.Name + ` СГРУППИРОВАТЬ ПО Склад`},
			{"документ_с_алиасом", `ВЫБРАТЬ Склад, СУММА(Количество) КАК К
				ИЗ Документ.` + doc.Name + ` КАК Д СГРУППИРОВАТЬ ПО Склад`},
			// Следующая секция сразу за полем: все синонимы HAVING и УПОРЯДОЧИТЬ.
			{"имеющие", `ВЫБРАТЬ Склад, СУММА(Количество) КАК К
				ИЗ РегистрНакопления.` + reg.Name + `
				СГРУППИРОВАТЬ ПО Склад ИМЕЮЩИЕ СУММА(Количество) > 0 УПОРЯДОЧИТЬ ПО Склад`},
			{"имея", `ВЫБРАТЬ Склад, СУММА(Количество) КАК К
				ИЗ РегистрНакопления.` + reg.Name + `
				СГРУППИРОВАТЬ ПО Склад ИМЕЯ СУММА(Количество) > 0 УПОРЯДОЧИТЬ ПО Склад`},
			{"having", `ВЫБРАТЬ Склад, СУММА(Количество) КАК К
				ИЗ РегистрНакопления.` + reg.Name + `
				СГРУППИРОВАТЬ ПО Склад HAVING СУММА(Количество) > 0 УПОРЯДОЧИТЬ ПО Склад`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if got := run(t, tc.src); strings.Join([]string{"[", strings.Join(got, " "), "]"}, "") != want {
					t.Fatalf("строки группировки %v, ожидалось %s: одноимённые склады склеены", got, want)
				}
			})
		}

		for _, tc := range []struct{ name, projection, column string }{
			{"select_all", "Склад", "склад"},
			{"select_all_expression", `ЕСТЬNULL(Склад, "") КАК Имя`, "имя"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				compiled, err := query.Compile(`SELECT ALL `+tc.projection+` FROM Document.`+doc.Name, opts)
				if err != nil {
					t.Fatal(err)
				}
				rows, cols, err := query.Run(ctx, db, &compiled)
				if err != nil {
					t.Fatalf("исполнение: %v\nSQL: %s", err, compiled.SQL)
				}
				if len(cols) != 1 || cols[0] != tc.column || len(rows) != 2 {
					t.Fatalf("SELECT ALL: cols=%v rows=%v", cols, rows)
				}
				for _, row := range rows {
					if row[tc.column] != "Основной" {
						t.Fatalf("SELECT ALL потерял поле %s: %v", tc.column, row)
					}
				}
			})
		}

		// Ссылка внутри выражения группировки: SELECT строит то же выражение
		// по представлению, поэтому и группировка идёт по нему — иначе
		// PostgreSQL отверг бы запрос. Здесь важно, что запрос исполняется.
		t.Run("выражение", func(t *testing.T) {
			got := run(t, `ВЫБРАТЬ ЕСТЬNULL(Склад, "") КАК С, СУММА(Количество) КАК К
				ИЗ РегистрНакопления.`+reg.Name+` СГРУППИРОВАТЬ ПО ЕСТЬNULL(Склад, "")`)
			if len(got) != 1 || got[0] != "Основной=13" {
				t.Fatalf("группировка по выражению: %v", got)
			}
		})
	})
}
