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

func TestRefJoinAliasCollisionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := []*metadata.Entity{
			{Name: "M", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Code", Type: metadata.FieldTypeString}}},
			{Name: "J1", Kind: metadata.KindCatalog, Fields: []metadata.Field{
				{Name: "Code", Type: metadata.FieldTypeString},
				{Name: "C", Type: "reference:T1", RefEntity: "T1"},
				{Name: "A_B_C", Type: "reference:T2", RefEntity: "T2"},
			}},
			{Name: "J2", Kind: metadata.KindCatalog, Fields: []metadata.Field{
				{Name: "Code", Type: metadata.FieldTypeString},
				{Name: "B_C", Type: "reference:T2", RefEntity: "T2"},
			}},
			{Name: "T1", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}},
			{Name: "T2", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}},
		}
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatal(err)
		}
		one, two := uuid.New(), uuid.New()
		for _, seed := range []struct {
			entity int
			id     uuid.UUID
			values map[string]any
		}{
			{0, uuid.New(), map[string]any{"Code": "GL"}},
			{3, one, map[string]any{"Наименование": "One"}},
			{4, two, map[string]any{"Наименование": "Two"}},
			{1, uuid.New(), map[string]any{"Code": "GL", "C": one, "A_B_C": two}},
			{2, uuid.New(), map[string]any{"Code": "GL", "B_C": two}},
		} {
			ent := ents[seed.entity]
			if err := db.Upsert(ctx, ent.Name, seed.id, seed.values, ent); err != nil {
				t.Fatal(err)
			}
		}
		const outer = ` ИЗ Справочник.M КАК M ЛЕВОЕ СОЕДИНЕНИЕ Справочник.J1 КАК A_B ПО A_B.Code = M.Code`
		const both = outer + ` ЛЕВОЕ СОЕДИНЕНИЕ Справочник.J2 КАК A ПО A.Code = M.Code`
		const pair = `ВЫБРАТЬ A_B.C.Наименование КАК First, A.B_C.Наименование КАК Second`
		const correlated = `ВЫБРАТЬ M.Code КАК Code` + outer + ` ГДЕ M.Code В (ВЫБРАТЬ I.Code ИЗ Справочник.M КАК I ЛЕВОЕ СОЕДИНЕНИЕ Справочник.J2 КАК A ПО A.Code = I.Code ГДЕ A_B.C.Наименование = "One" И A.B_C.Наименование = "Two")`
		longAlias := strings.Repeat("a", 57)
		for _, tc := range []struct {
			name, text string
			want       []map[string]any
		}{
			{"joined-pairs", pair + both, []map[string]any{{"first": "One", "second": "Two"}}},
			{"repeated-path", pair + both + ` ГДЕ A_B.C.Наименование = "One" И A.B_C.Наименование = "Two"`, []map[string]any{{"first": "One", "second": "Two"}}},
			{"correlated-pairs", correlated, []map[string]any{{"code": "GL"}}},
			{"correlated-outer-mismatch", strings.Replace(correlated, `= "One"`, `= "Two"`, 1), nil},
			{"correlated-inner-mismatch", strings.Replace(correlated, `= "Two"`, `= "One"`, 1), nil},
			{"main-and-joined", `ВЫБРАТЬ Main.A_B_C.Наименование КАК First, A_B.C.Наименование КАК Second ИЗ Справочник.J1 КАК Main ЛЕВОЕ СОЕДИНЕНИЕ Справочник.J1 КАК A_B ПО A_B.Code = Main.Code`, []map[string]any{{"first": "Two", "second": "One"}}},
			{"explicit-preferred", pair + both + ` ЛЕВОЕ СОЕДИНЕНИЕ Справочник.M КАК ref_a_b_c ПО ref_a_b_c.Code = M.Code`, []map[string]any{{"first": "One", "second": "Two"}}},
			{"explicit-fallback", pair + both + ` ЛЕВОЕ СОЕДИНЕНИЕ Справочник.M КАК ref_join_1 ПО ref_join_1.Code = M.Code`, []map[string]any{{"first": "One", "second": "Two"}}},
			{"derived-preferred", pair + both + ` ЛЕВОЕ СОЕДИНЕНИЕ (ВЫБРАТЬ Code ИЗ Справочник.M) КАК ref_a_b_c ПО ref_a_b_c.Code = M.Code`, []map[string]any{{"first": "One", "second": "Two"}}},
			{"nested-explicit-shadow", `ВЫБРАТЬ M.Code КАК Code` + outer + ` ГДЕ M.Code В (ВЫБРАТЬ ref_a_b_c.Code ИЗ Справочник.M КАК ref_a_b_c ГДЕ A_B.C.Наименование = "One")`, []map[string]any{{"code": "GL"}}},
			{"nested-derived-shadow", `ВЫБРАТЬ M.Code КАК Code` + outer + ` ГДЕ M.Code В (ВЫБРАТЬ ref_a_b_c.Code ИЗ (ВЫБРАТЬ Code ИЗ Справочник.M) КАК ref_a_b_c ГДЕ A_B.C.Наименование = "One")`, []map[string]any{{"code": "GL"}}},
			{"main-explicit-preferred", `ВЫБРАТЬ Main.C.Наименование КАК First, ref_c.Code КАК Second ИЗ Справочник.J1 КАК Main ЛЕВОЕ СОЕДИНЕНИЕ Справочник.M КАК ref_c ПО ref_c.Code = Main.Code`, []map[string]any{{"first": "One", "second": "GL"}}},
			{"postgres-truncated-explicit", `ВЫБРАТЬ ` + longAlias + `.C.Наименование КАК Value ИЗ Справочник.M КАК M ЛЕВОЕ СОЕДИНЕНИЕ Справочник.J1 КАК ` + longAlias + ` ПО ` + longAlias + `.Code = M.Code ЛЕВОЕ СОЕДИНЕНИЕ Справочник.M КАК ref_` + longAlias + `_c_suffix ПО 1 = 1`, []map[string]any{{"value": "One"}}},
			{"long-generated", `ВЫБРАТЬ ` + longAlias + `long.C.Наименование КАК Value ИЗ Справочник.M КАК M ЛЕВОЕ СОЕДИНЕНИЕ Справочник.J1 КАК ` + longAlias + `long ПО ` + longAlias + `long.Code = M.Code`, []map[string]any{{"value": "One"}}},
			{"derived-joined-pairs", `ВЫБРАТЬ D.First КАК First, D.Second КАК Second ИЗ (` + pair + both + `) КАК D`, []map[string]any{{"first": "One", "second": "Two"}}},
			{"union-siblings", `ВЫБРАТЬ A_B.C.Наименование КАК Value` + outer + ` ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ A.B_C.Наименование КАК Value ИЗ Справочник.M КАК M ЛЕВОЕ СОЕДИНЕНИЕ Справочник.J2 КАК A ПО A.Code = M.Code`, []map[string]any{{"value": "One"}, {"value": "Two"}}},
			{"left-join-missing", `ВЫБРАТЬ M.Code КАК Code, A_B.C.Наименование КАК Value ИЗ Справочник.M КАК M ЛЕВОЕ СОЕДИНЕНИЕ Справочник.J1 КАК A_B ПО A_B.Code = "missing"`, []map[string]any{{"code": "GL", "value": nil}}},
			{"colliding-left-join-missing", pair + strings.Replace(both, "A_B.Code = M.Code", `A_B.Code = "missing"`, 1), []map[string]any{{"first": nil, "second": "Two"}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				r, err := query.Compile(tc.text, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
				if err != nil {
					t.Fatal(err)
				}
				rows, _, err := query.Run(ctx, db, &r)
				if err != nil {
					t.Fatalf("Run: %v\nSQL: %s", err, r.SQL)
				}
				if len(rows) != len(tc.want) || len(rows) > 0 && !reflect.DeepEqual(rows, tc.want) {
					t.Fatalf("got %#v, want %#v\nSQL: %s", rows, tc.want, r.SQL)
				}
			})
		}
	})
}
