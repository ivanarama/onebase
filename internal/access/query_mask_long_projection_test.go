package access_test

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// A field in another source must not rename a selected, independently valid
// short column. Exercise the same query and field guard used by consumers.
func TestQueryMaskLongProjection_Matrix(t *testing.T) {
	const longField = "ДополнительныйСтроковыйРеквизитДляПроверкиПроекции"
	const longAlias = "ПолноеИмяВыбраннойКолонкиДляПроверкиПроекции"
	short := metadata.SQLIdent(strings.ToLower(longField))
	client := &metadata.Entity{Name: "Клиент", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: short, Type: metadata.FieldTypeString}}}
	other := &metadata.Entity{Name: "Дополнение", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: longField, Type: metadata.FieldTypeString}}}
	entities := []*metadata.Entity{client, other}
	if err := metadata.Validate(entities, nil); err != nil {
		t.Fatal(err)
	}
	lookup := func(_, name string) *metadata.Entity {
		for _, e := range entities {
			if strings.EqualFold(e.Name, name) {
				return e
			}
		}
		return nil
	}
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		id := uuid.New()
		for _, e := range entities {
			if err := db.Upsert(ctx, e.Name, id, map[string]any{e.Fields[0].Name: "значение1234"}, e); err != nil {
				t.Fatal(err)
			}
		}
		cases := []struct {
			name, selection, from, key string
			star                       bool
		}{
			{"joined-star", "К.*", "Справочник.Клиент КАК К ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Дополнение КАК Д ПО К.Ссылка = Д.Ссылка", short, true},
			{"joined-field", "К." + short, "Справочник.Клиент КАК К ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Дополнение КАК Д ПО К.Ссылка = Д.Ссылка", short, false},
			{"joined-short-alias", "К." + short + " КАК " + short, "Справочник.Клиент КАК К ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Дополнение КАК Д ПО К.Ссылка = Д.Ссылка", short, false},
			{"selected-long-field", "Д." + longField, "Справочник.Дополнение КАК Д ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Клиент КАК К ПО К.Ссылка = Д.Ссылка", strings.ToLower(longField), false},
			{"selected-long-star", "Д.*", "Справочник.Клиент КАК К ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Дополнение КАК Д ПО К.Ссылка = Д.Ссылка", strings.ToLower(longField), true},
			{"selected-long-alias", "К." + short + " КАК " + longAlias, "Справочник.Клиент КАК К ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Дополнение КАК Д ПО К.Ссылка = Д.Ссылка", strings.ToLower(longAlias), false},
			{"single-source", "К.*", "Справочник.Клиент КАК К", short, true},
		}
		// When two selected outputs themselves share a physical label, there
		// is no source identity left in the driver's row map. The established
		// complex-projection guard must deny protected data, rather than guess.
		for _, selection := range []string{"*", "К.*, Д.*", "К." + short + ", Д." + longField} {
			res, err := query.Compile("ВЫБРАТЬ "+selection+" ИЗ Справочник.Клиент КАК К ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Дополнение КАК Д ПО К.Ссылка=Д.Ссылка", query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
			if err != nil {
				t.Fatal(err)
			}
			for _, protected := range []string{short, longField} {
				user := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{Catalogs: map[string][]string{client.Name: {"read"}, other.Name: {"read"}}, FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{client.Name: {protected: {Read: "hide"}}, other.Name: {protected: {Read: "hide"}}}}}}}}
				if plan := access.QueryMaskPlanFor(user, res, lookup); plan.Denied == "" {
					t.Errorf("ambiguous %s: protected query was permitted", selection)
				}
			}
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				for _, policy := range []string{"hide", "mask_tail", "full"} {
					t.Run(policy, func(t *testing.T) {
						clientPolicies, otherPolicies := auth.FieldPolicies{}, auth.FieldPolicies{}
						if tc.key == strings.ToLower(longField) {
							otherPolicies[longField] = auth.FieldPolicy{Read: policy, Keep: 4}
						} else {
							clientPolicies[short] = auth.FieldPolicy{Read: policy, Keep: 4}
						}
						user := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{
							Catalogs:    map[string][]string{client.Name: {"read"}, other.Name: {"read"}},
							FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{client.Name: clientPolicies, other.Name: otherPolicies}},
						}}}}
						res, err := query.Compile("ВЫБРАТЬ "+tc.selection+" ИЗ "+tc.from, query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
						if err != nil {
							t.Fatal(err)
						}
						plan := access.QueryMaskPlanFor(user, res, lookup)
						if plan.Denied != "" {
							t.Fatal(plan.Denied)
						}
						// Reuse the compiled result, including its restoration map, on both public
						// execution paths; restoration must remain idempotent for DSL callers.
						for _, limited := range []bool{false, true} {
							var rows []map[string]any
							var cols []string
							if limited {
								var truncated bool
								rows, cols, truncated, err = query.RunLimit(ctx, db, &res, 10)
								if truncated {
									t.Fatal("unexpected truncation")
								}
							} else {
								rows, cols, err = query.Run(ctx, db, &res)
							}
							if err != nil {
								t.Fatal(err)
							}
							wantKeys := []string{tc.key}
							if tc.star {
								wantKeys = append(wantKeys, "id", "_version", "deletion_mark")
							}
							sort.Strings(wantKeys)
							gotKeys := append([]string(nil), cols...)
							sort.Strings(gotKeys)
							if !reflect.DeepEqual(gotKeys, wantKeys) {
								t.Errorf("columns = %v; want %v", cols, wantKeys)
							}
							if len(rows) != 1 {
								t.Fatalf("rows = %v", rows)
							}
							query.RestoreLongLabels(&res, rows, cols)
							tracked, err := plan.ApplyTracked(rows)
							if err != nil {
								t.Fatal(err)
							}
							want := any("значение1234")
							if policy == "hide" {
								want = nil
							}
							if policy == "mask_tail" {
								want = "••••••••1234"
							}
							value, ok := rows[0][tc.key]
							if !ok || value != want {
								t.Errorf("result = %v; want %s=%v", rows, tc.key, want)
							}
							rowKeys := make([]string, 0, len(rows[0]))
							for key := range rows[0] {
								rowKeys = append(rowKeys, key)
							}
							sort.Strings(rowKeys)
							if !reflect.DeepEqual(rowKeys, wantKeys) {
								t.Errorf("row keys = %v; want %v", rowKeys, wantKeys)
							}
							if policy == "full" {
								if len(tracked) != 0 {
									t.Errorf("full reader tracked = %v", tracked)
								}
							} else if len(tracked) != 1 || len(tracked[0]) != 1 {
								t.Errorf("tracked = %v", tracked)
							} else if _, ok := tracked[0][tc.key]; !ok {
								t.Errorf("tracked = %v; want %s", tracked, tc.key)
							}
							// Apply is the non-tracking entry point used by ordinary consumers.
							if err := plan.Apply(rows); err != nil {
								t.Fatal(err)
							}
						}
					})
				}
			})
		}
	})
}
