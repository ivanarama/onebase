package query_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Exercise the review reproduction through the public compiler and runner.
// SELECT modifiers change neither output names nor the visibility of an outer reference.
func TestDerivedProjectionSelectModifiersMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := []*metadata.Entity{
			{Name: "Филиалы", Kind: metadata.KindCatalog, Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Owner", Type: metadata.FieldTypeString},
			}},
			{Name: "Склады", Kind: metadata.KindCatalog, Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Филиал", Type: "reference:Филиалы", RefEntity: "Филиалы"},
				{Name: "Owner", Type: metadata.FieldTypeString},
			}},
			{Name: "Локальные", Kind: metadata.KindCatalog, Fields: []metadata.Field{
				{Name: "Филиал", Type: metadata.FieldTypeString},
			}},
		}
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatal(err)
		}
		branch := uuid.New()
		if err := db.Upsert(ctx, ents[0].Name, branch, map[string]any{"Наименование": "Ф-А", "Owner": "own"}, ents[0]); err != nil {
			t.Fatal(err)
		}
		for _, row := range []struct {
			name, owner string
			ref         uuid.UUID
		}{
			{"С-А", "own", branch}, {"С-Б", "other", branch}, {"С-В", "own", uuid.Nil},
		} {
			values := map[string]any{"Наименование": row.name, "Owner": row.owner}
			if row.ref != uuid.Nil {
				values["Филиал"] = row.ref
			}
			if err := db.Upsert(ctx, ents[1].Name, uuid.New(), values, ents[1]); err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range []struct{ name, projection string }{
			{"plain", `ВЫБРАТЬ Ф.Наименование ИЗ Справочник.Филиалы КАК Ф`},
			{"all", `ВЫБРАТЬ ВСЕ Ф.Наименование ИЗ Справочник.Филиалы КАК Ф`},
			{"all_star", `ВЫБРАТЬ ВСЕ Ф.* ИЗ Справочник.Филиалы КАК Ф`},
			{"all_unqualified_star", `ВЫБРАТЬ ВСЕ * ИЗ Справочник.Филиалы КАК Ф`},
			{"english_all", `SELECT ALL Ф.Наименование FROM Справочник.Филиалы AS Ф`},
			{"english_all_star", `SELECT ALL Ф.* FROM Справочник.Филиалы AS Ф`},
			{"distinct", `ВЫБРАТЬ РАЗЛИЧНЫЕ Ф.Наименование ИЗ Справочник.Филиалы КАК Ф`},
			{"english_distinct", `SELECT DISTINCT Ф.Наименование FROM Справочник.Филиалы AS Ф`},
			{"all_alias", `ВЫБРАТЬ ВСЕ Ф.Наименование КАК Название ИЗ Справочник.Филиалы КАК Ф`},
			{"all_bridge", `ВЫБРАТЬ ВСЕ * ИЗ (ВЫБРАТЬ ВСЕ Ф.* ИЗ Справочник.Филиалы КАК Ф) КАК Е`},
			{"all_union_first_output", `ВЫБРАТЬ ВСЕ Ф.Наименование КАК Один ИЗ Справочник.Филиалы КАК Ф ОБЪЕДИНИТЬ ВЫБРАТЬ Ф.Наименование КАК Филиал ИЗ Справочник.Филиалы КАК Ф`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				text := `ВЫБРАТЬ С.Наименование КАК Имя ИЗ Справочник.Склады КАК С ГДЕ "Ф-А" = (ВЫБРАТЬ Филиал.Наименование ИЗ (` + tc.projection + `) КАК П) УПОРЯДОЧИТЬ ПО Имя`
				run := func(opts query.CompileOpts, want []string) {
					t.Helper()
					res, err := query.Compile(text, opts)
					if err != nil {
						t.Fatal(err)
					}
					rows, cols, err := query.Run(ctx, db, &res)
					if err != nil {
						t.Fatalf("run: %v\nSQL: %s", err, res.SQL)
					}
					if !reflect.DeepEqual(cols, []string{"имя"}) {
						t.Fatalf("columns=%v; want [имя]", cols)
					}
					var got []string
					for _, row := range rows {
						got = append(got, fmt.Sprint(row["имя"]))
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("rows=%v; want %v\nSQL: %s", got, want, res.SQL)
					}
					for _, e := range ents[:2] {
						found := false
						for _, source := range res.Sources {
							if source.Kind == "catalog" && source.Name == e.Name {
								found = true
							}
						}
						if !found {
							t.Fatalf("missing RBAC source %s: %v", e.Name, res.Sources)
						}
					}
				}
				opts := query.CompileOpts{Entities: ents, Dialect: db.Dialect()}
				run(opts, []string{"С-А", "С-Б"})
				// The same projection must not bypass the warehouse or reference-target policy.
				opts.RowFilters = map[query.SourceRef]*storage.Predicate{
					{Kind: "catalog", Name: "Склады"}:  {Field: "Owner", Op: "eq", Value: "own"},
					{Kind: "catalog", Name: "Филиалы"}: {Field: "Owner", Op: "eq", Value: "own"},
				}
				run(opts, []string{"С-А"})
				opts.RowFilters[query.SourceRef{Kind: "catalog", Name: "Филиалы"}] = &storage.Predicate{Field: "Owner", Op: "eq", Value: "denied"}
				run(opts, nil)
			})
		}
		// Declared local names and unknown expressions still stop outer lookup.
		// Invalid navigation on their scalar output must fail rather than expose a reference.
		for _, tc := range []struct{ name, projection string }{
			{"all_alias", `ВЫБРАТЬ ВСЕ 1 КАК Филиал`},
			{"all_field", `ВЫБРАТЬ ВСЕ Л.Филиал ИЗ Справочник.Локальные КАК Л`},
			{"all_star", `ВЫБРАТЬ ВСЕ * ИЗ Справочник.Локальные КАК Л`},
			{"all_qualified_star", `ВЫБРАТЬ ВСЕ Л.* ИЗ Справочник.Локальные КАК Л`},
			{"english_all_star", `SELECT ALL Л.* FROM Справочник.Локальные AS Л`},
			{"all_nested_star", `ВЫБРАТЬ ВСЕ * ИЗ (ВЫБРАТЬ ВСЕ 1 КАК Филиал) КАК Л`},
			{"all_union_first_output", `ВЫБРАТЬ ВСЕ 1 КАК Филиал ОБЪЕДИНИТЬ ВЫБРАТЬ 1 КАК Один`},
			{"all_unknown_expression", `ВЫБРАТЬ ВСЕ СУММА(1)`},
		} {
			t.Run("shadow/"+tc.name, func(t *testing.T) {
				text := `ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование ИЗ (` + tc.projection + `) КАК П) КАК Имя ИЗ Справочник.Склады КАК С`
				res, err := query.Compile(text, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(res.SQL, "ref_филиал") {
					t.Fatalf("local output borrowed outer reference: %s", res.SQL)
				}
				if _, _, err := query.Run(ctx, db, &res); err == nil {
					t.Fatalf("invalid scalar navigation succeeded: %s", res.SQL)
				}
			})
		}
	})
}
