package query_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Совмещение #1459 с учётом типов источников: навигационный auto-JOIN
// не должен менять владельца строкового поля или терять NULL-строки LEFT JOIN.
func TestJoinedRefTextComparisonMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := joinedRefEntities()
		ents[0].Fields = append(ents[0].Fields, metadata.Field{Name: "Статус", Type: metadata.FieldTypeNumber})
		ents[1].Fields = append(ents[1].Fields, metadata.Field{Name: "Статус", Type: metadata.FieldTypeString})
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatal(err)
		}
		seedJoinedRef(t, ctx, db, ents)
		if err := db.Upsert(ctx, "Базы", uuid.New(), map[string]any{"Код": "NO", "Наименование": "Без плана"}, ents[0]); err != nil {
			t.Fatal(err)
		}
		const projection = `ВЫБРАТЬ Б.Код КАК Метка, П.ГлавныйУзел.Наименование КАК Узел`
		const direct = ` ИЗ Справочник.Базы КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.ГлавныйУзел = Б.Ссылка`
		const derived = ` ИЗ (ВЫБРАТЬ Ссылка, Код, Статус ИЗ Справочник.Базы) КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.ГлавныйУзел = Б.Ссылка`
		const filter = ` ГДЕ П.Статус <> "Завершено"`
		for _, tc := range []struct {
			name, text string
			want       map[string]any
		}{
			{"joined-text-keeps-unmatched", projection + direct + filter, map[string]any{"GL": "Главная", "NO": nil}},
			{"derived-first-keeps-source-role", projection + derived + filter, map[string]any{"GL": "Главная", "NO": nil}},
			{"nested-select-keeps-navigation-and-text", `ВЫБРАТЬ Под.Метка, Под.Узел ИЗ (` + projection + direct + filter + `) КАК Под`, map[string]any{"GL": "Главная", "NO": nil}},
			{"union-keeps-both-source-indices", projection + direct + filter + ` ОБЪЕДИНИТЬ ` + projection + derived + filter, map[string]any{"GL": "Главная", "NO": nil}},
			// Одноимённое числовое поле главного источника не получает
			// строковое правило присоединённого: NULL <> 0 не истинно.
			{"main-number-does-not-borrow-joined-text-type", projection + direct + filter + ` И Б.Статус <> 0`, map[string]any{}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				r, err := query.Compile(tc.text, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
				if err != nil {
					t.Fatal("Compile:", err)
				}
				rows, _, err := query.Run(ctx, db, &r)
				if err != nil {
					t.Fatalf("Run: %v\nSQL: %s", err, r.SQL)
				}
				got := map[string]any{}
				for _, row := range rows {
					key, ok := row["метка"].(string)
					if !ok {
						t.Fatalf("unexpected row: %v", row)
					}
					if _, exists := got[key]; exists {
						t.Fatalf("duplicate row: %v\nSQL: %s", row, r.SQL)
					}
					got[key] = row["узел"]
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("got %v, want %v\nSQL: %s", got, tc.want, r.SQL)
				}
			})
		}
	})
}
