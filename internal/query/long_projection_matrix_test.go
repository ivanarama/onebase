package query_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestLongOutputLabels_Matrix(t *testing.T) {
	const longName = "НейтральноеДлинноеИмяДляПроверкиВыходнойКолонки"
	logical := strings.ToLower(longName)
	short := metadata.SQLIdent(logical)
	source := &metadata.Entity{Name: "Проекция", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: short, Type: metadata.FieldTypeString}}}
	// These independent fields cannot coexist in a single physical table. Keep
	// them in separate sources, as a valid configuration is allowed to do.
	other := &metadata.Entity{Name: longName, Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Текст", Type: metadata.FieldTypeString}}}
	longSource := &metadata.Entity{Name: "ДлинноеПоле", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: longName, Type: metadata.FieldTypeString}}}
	entities := []*metadata.Entity{source, other, longSource}
	if err := metadata.Validate(entities, nil); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, src, key string }{
		{"unselected-table-name", "ВЫБРАТЬ П." + short + " ИЗ Справочник.Проекция КАК П ЛЕВОЕ СОЕДИНЕНИЕ Справочник." + longName + " КАК Д ПО П.Ссылка=Д.Ссылка", short},
		{"unselected-source-alias", "ВЫБРАТЬ П." + short + " ИЗ Справочник.Проекция КАК П ЛЕВОЕ СОЕДИНЕНИЕ Справочник.ДлинноеПоле КАК " + longName + " ПО П.Ссылка=" + longName + ".Ссылка", short},
		{"unselected-predicate", "ВЫБРАТЬ П." + short + " ИЗ Справочник.Проекция КАК П ЛЕВОЕ СОЕДИНЕНИЕ Справочник.ДлинноеПоле КАК Д ПО П.Ссылка=Д.Ссылка ГДЕ Д." + longName + " = &Значение", short},
		{"parenthesized-long", "ВЫБРАТЬ ((Д." + longName + ")) ИЗ Справочник.ДлинноеПоле КАК Д", logical},
		{"union-first-short", "ВЫБРАТЬ " + short + " ИЗ Справочник.Проекция ОБЪЕДИНИТЬ ВЫБРАТЬ " + longName + " ИЗ Справочник.ДлинноеПоле", short},
		{"union-first-long", "ВЫБРАТЬ " + longName + " ИЗ Справочник.ДлинноеПоле ОБЪЕДИНИТЬ ВЫБРАТЬ " + short + " ИЗ Справочник.Проекция", logical},
		{"derived-short", "ВЫБРАТЬ Выб." + short + " ИЗ (ВЫБРАТЬ П." + short + " ИЗ Справочник.Проекция КАК П ЛЕВОЕ СОЕДИНЕНИЕ Справочник.ДлинноеПоле КАК Д ПО П.Ссылка=Д.Ссылка) КАК Выб", short},
		{"derived-long-field", "ВЫБРАТЬ Выб." + longName + " ИЗ (ВЫБРАТЬ " + longName + " ИЗ Справочник.ДлинноеПоле) КАК Выб", logical},
		{"derived-long-star", "ВЫБРАТЬ Выб.* ИЗ (ВЫБРАТЬ " + longName + " ИЗ Справочник.ДлинноеПоле) КАК Выб", logical},
		{"derived-long-alias", "ВЫБРАТЬ Выб.* ИЗ (ВЫБРАТЬ " + short + " КАК " + longName + " ИЗ Справочник.Проекция) КАК Выб", logical},
	}
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		id := uuid.New()
		for _, e := range entities {
			if err := db.Upsert(ctx, e.Name, id, map[string]any{e.Fields[0].Name: "пример"}, e); err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				res, err := query.Compile(tc.src, query.CompileOpts{Entities: entities, Dialect: db.Dialect(), Params: map[string]any{"Значение": "пример"}})
				if err != nil {
					t.Fatal(err)
				}
				for _, run := range []func(context.Context, *storage.DB, *query.Result) ([]map[string]any, []string, error){
					func(ctx context.Context, db *storage.DB, res *query.Result) ([]map[string]any, []string, error) {
						return query.Run(ctx, db, res)
					},
					func(ctx context.Context, db *storage.DB, res *query.Result) ([]map[string]any, []string, error) {
						rows, cols, _, err := query.RunLimit(ctx, db, res, 2)
						return rows, cols, err
					},
					func(ctx context.Context, db *storage.DB, res *query.Result) ([]map[string]any, []string, error) {
						return query.RunPage(ctx, db, res, 2, 0)
					},
				} {
					for range 2 {
						rows, cols, err := run(ctx, db, &res)
						if err != nil {
							t.Fatalf("%v\nSQL: %s", err, res.SQL)
						}
						if !reflect.DeepEqual(cols, []string{tc.key}) || len(rows) != 1 || len(rows[0]) != 1 || rows[0][tc.key] != "пример" {
							t.Errorf("cols=%v rows=%v; want %s=пример", cols, rows, tc.key)
						}
					}
				}
			})
		}
	})
}
