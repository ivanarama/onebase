package query_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Exercise the exported projection through all public executors: shortening
// an expression alias must not change its key or bypass date normalization.
func TestLongExpressionLabels_Matrix(t *testing.T) {
	const alias = "ДатаНачалаОченьДлинногоПериодаПроверкиИнтеграции"
	const longEntity = "ДлинноеИмяСправочникаДляПроверкиВыходныхПсевдонимов"
	const longField = "ДлинноеИмяРеквизитаДатыДляПроверкиВыходныхПсевдонимов"
	entities := []*metadata.Entity{
		{Name: "Периоды", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Момент", Type: metadata.FieldTypeDate}}},
		{Name: longEntity, Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: longField, Type: metadata.FieldTypeDate}}},
	}
	if err := metadata.Validate(entities, nil); err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	last := first.Add(24 * time.Hour)
	type testCase struct {
		name, sql, key string
		want           []any
	}
	cases := []testCase{
		{"minimum", "ВЫБРАТЬ МИНИМУМ(П.Момент) КАК " + alias + " ИЗ Справочник.Периоды КАК П", strings.ToLower(alias), []any{first}},
		{"maximum", "ВЫБРАТЬ МАКСИМУМ(П.Момент) КАК " + alias + " ИЗ Справочник.Периоды КАК П", strings.ToLower(alias), []any{last}},
		{"expression", "ВЫБРАТЬ 2 + 3 КАК " + alias + " ИЗ Справочник.Периоды КАК П", strings.ToLower(alias), []any{int64(5), int64(5)}},
		{"long-source-short-alias", "ВЫБРАТЬ МИНИМУМ(П." + longField + ") КАК Дата ИЗ Справочник." + longEntity + " КАК П", "дата", []any{first}},
		{"short-control", "ВЫБРАТЬ МАКСИМУМ(П.Момент) КАК Дата ИЗ Справочник.Периоды КАК П", "дата", []any{last}},
		{"ordered-pages", "ВЫБРАТЬ П.Момент КАК " + alias + " ИЗ Справочник.Периоды КАК П УПОРЯДОЧИТЬ ПО П.Момент", strings.ToLower(alias), []any{first, last}},
		{"union-first-expression", "ВЫБРАТЬ 2 + 3 КАК " + alias + " ИЗ Справочник.Периоды ОБЪЕДИНИТЬ ВЫБРАТЬ 2 + 3 КАК ДругоеИмя ИЗ Справочник.Периоды", strings.ToLower(alias), []any{int64(5)}},
		{"derived-expression-star", "ВЫБРАТЬ Выб.* ИЗ (ВЫБРАТЬ 2 + 3 КАК " + alias + " ИЗ Справочник.Периоды) КАК Выб", strings.ToLower(alias), []any{int64(5), int64(5)}},
	}
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		for _, entity := range entities {
			for _, moment := range []time.Time{first, last} {
				if err := db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{entity.Fields[0].Name: moment}, entity); err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				res, err := query.Compile(tc.sql, query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
				if err != nil {
					t.Fatal(err)
				}
				check := func(rows []map[string]any, cols []string, want []any) {
					t.Helper()
					if !reflect.DeepEqual(cols, []string{tc.key}) || len(rows) != len(want) {
						t.Fatalf("cols=%v rows=%v; want key=%s values=%v", cols, rows, tc.key, want)
					}
					for i, row := range rows {
						if len(row) != 1 {
							t.Fatalf("unexpected output keys: %v", row)
						}
						if moment, ok := want[i].(time.Time); ok {
							got, ok := row[tc.key].(time.Time)
							if !ok || !got.Equal(moment) {
								t.Fatalf("date=%#v (%T); want %v", row[tc.key], row[tc.key], moment)
							}
						} else if fmt.Sprint(row[tc.key]) != fmt.Sprint(want[i]) {
							t.Fatalf("value=%#v (%T); want %#v", row[tc.key], row[tc.key], want[i])
						}
					}
				}
				for range 2 {
					rows, cols, err := query.Run(ctx, db, &res)
					if err != nil {
						t.Fatal(err)
					}
					check(rows, cols, tc.want)
					rows, cols, truncated, err := query.RunLimit(ctx, db, &res, 1)
					if err != nil || truncated != (len(tc.want) > 1) {
						t.Fatalf("RunLimit: truncated=%v err=%v", truncated, err)
					}
					check(rows, cols, tc.want[:1])
					for offset := 0; offset <= len(tc.want); offset++ {
						rows, cols, err := query.RunPage(ctx, db, &res, 1, offset)
						if err != nil {
							t.Fatal(err)
						}
						end := min(offset+1, len(tc.want))
						check(rows, cols, tc.want[offset:end])
					}
				}
			})
		}
	})
}
