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

// These references occur only in a child's clause, so the outer SELECT must
// prepare its JOIN before emitting FROM, without borrowing a sibling's source.
func TestCorrelatedReferenceClausesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := []*metadata.Entity{
			{Name: "Филиалы", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Owner", Type: metadata.FieldTypeString}}},
			{Name: "Разделы", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Owner", Type: metadata.FieldTypeString}}},
			{Name: "Склады", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Филиал", Type: "reference:Филиалы", RefEntity: "Филиалы"}, {Name: "Owner", Type: metadata.FieldTypeString}}},
			{Name: "Организации", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Филиал", Type: "reference:Разделы", RefEntity: "Разделы"}, {Name: "Owner", Type: metadata.FieldTypeString}}},
			{Name: "Локальные", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Филиал", Type: metadata.FieldTypeString}}},
		}
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatal(err)
		}
		insert := func(e *metadata.Entity, name, owner string, ref uuid.UUID) uuid.UUID {
			t.Helper()
			id := uuid.New()
			values := map[string]any{"Наименование": name, "Owner": owner}
			if ref != uuid.Nil {
				values["Филиал"] = ref
			}
			if err := db.Upsert(ctx, e.Name, id, values, e); err != nil {
				t.Fatal(err)
			}
			return id
		}
		first := insert(ents[0], "Ф-Б", "own", uuid.Nil)
		second := insert(ents[0], "Ф-А", "other", uuid.Nil)
		twin := insert(ents[0], "Ф-Б", "own", uuid.Nil) // same display, different FK
		division := insert(ents[1], "Р-А", "own", uuid.Nil)
		insert(ents[2], "С-А", "own", first)
		insert(ents[2], "С-Б", "own", second)
		insert(ents[2], "С-В", "other", first) // same FK, different source policy
		insert(ents[2], "С-Г", "own", twin)
		insert(ents[3], "О-А", "own", division)
		if err := db.Upsert(ctx, ents[4].Name, uuid.New(), map[string]any{"Филиал": "Локально"}, ents[4]); err != nil {
			t.Fatal(err)
		}
		opts := query.CompileOpts{Entities: ents, Dialect: db.Dialect()}
		filtered := opts
		filtered.RowFilters = map[query.SourceRef]*storage.Predicate{}
		for _, e := range ents[:4] {
			filtered.RowFilters[query.SourceRef{Kind: "catalog", Name: e.Name}] = &storage.Predicate{Field: "Owner", Op: "eq", Value: "own"}
		}
		run := func(t *testing.T, text string, local query.CompileOpts, want []string) query.Result {
			t.Helper()
			res, err := query.Compile(text, local)
			if err != nil {
				t.Fatal(err)
			}
			rows, cols, err := query.Run(ctx, db, &res)
			if err != nil {
				t.Fatalf("run: %v\nSQL: %s", err, res.SQL)
			}
			if !reflect.DeepEqual(cols, []string{"имя"}) {
				t.Fatalf("columns = %v; want [имя]", cols)
			}
			var got []string
			for _, row := range rows {
				got = append(got, fmt.Sprint(row["имя"]))
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("rows = %v; want %v\nSQL: %s", got, want, res.SQL)
			}
			return res
		}
		outer := func(child string) string {
			return `ВЫБРАТЬ С.Наименование КАК Имя ИЗ Справочник.Склады КАК С ГДЕ 1 = (` + child + `) УПОРЯДОЧИТЬ ПО Имя`
		}
		for _, child := range []string{
			`ВЫБРАТЬ 1 КАК Филиал ОБЪЕДИНИТЬ ВЫБРАТЬ 1 КАК ДругоеИмя УПОРЯДОЧИТЬ ПО Филиал`,
			`ВЫБРАТЬ 1 КАК Филиал УПОРЯДОЧИТЬ ПО Филиал`,
			`ВЫБРАТЬ 1 КАК Филиал СГРУППИРОВАТЬ ПО Филиал`,
		} {
			t.Run("output alias/"+child, func(t *testing.T) {
				res := run(t, outer(child), opts, []string{"С-А", "С-Б", "С-В", "С-Г"})
				for _, source := range res.Sources {
					if source.Name == "Филиалы" {
						t.Fatalf("output alias borrowed outer target: %v\nSQL: %s", res.Sources, res.SQL)
					}
				}
			})
		}

		froms := []struct{ name, text string }{
			{"no source", ""},
			{"derived", ` ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П`},
			{"physical column collision", ` ИЗ (ВЫБРАТЬ "local" КАК филиал_id) КАК П`},
			{"first union output absent", ` ИЗ (ВЫБРАТЬ 1 КАК Один ОБЪЕДИНИТЬ ВЫБРАТЬ 1 КАК Филиал) КАК П`},
			{"proven star absent", ` ИЗ (ВЫБРАТЬ ВСЕ Ф.* ИЗ Справочник.Филиалы КАК Ф ГДЕ Ф.Наименование = "Ф-А") КАК П`},
		}
		for _, from := range froms {
			for _, bridge := range []bool{false, true} {
				wrap := func(child string) string {
					if bridge {
						return `ВЫБРАТЬ (` + child + `) ИЗ (SELECT ALL 1 КАК Один) КАК Е`
					}
					return child
				}
				for _, clause := range []string{"СГРУППИРОВАТЬ ПО", "УПОРЯДОЧИТЬ ПО"} {
					t.Run(fmt.Sprintf("display/%s/%t/%s", from.name, bridge, clause), func(t *testing.T) {
						res := run(t, outer(wrap(`ВЫБРАТЬ 1`+from.text+` `+clause+` Филиал`)), opts, []string{"С-А", "С-Б", "С-В", "С-Г"})
						// Execution alone cannot distinguish grouping/sorting a
						// constant outer FK from its display: assert the public SQL.
						keyword := "GROUP BY"
						if clause == "УПОРЯДОЧИТЬ ПО" {
							keyword = "ORDER BY"
						}
						if !strings.Contains(res.SQL, keyword+" ref_филиал.наименование") {
							t.Fatalf("clause lost display semantics: %s", res.SQL)
						}
					})
				}
				for _, param := range []struct {
					name string
					id   uuid.UUID
					want []string
				}{
					{"first", first, []string{"С-А", "С-В"}},
					{"second", second, []string{"С-Б"}},
					{"same display", twin, []string{"С-Г"}},
					{"missing", uuid.New(), nil},
				} {
					t.Run(fmt.Sprintf("having/%s/%t/%s", from.name, bridge, param.name), func(t *testing.T) {
						local := opts
						local.Params = map[string]any{"Филиал": param.id}
						run(t, outer(wrap(`ВЫБРАТЬ КОЛИЧЕСТВО(1)`+from.text+` ИМЕЮЩИЕ Филиал = &Филиал`)), local, param.want)
					})
				}
			}
		}
		for _, clause := range []string{"СГРУППИРОВАТЬ ПО", "УПОРЯДОЧИТЬ ПО", "ИМЕЮЩИЕ"} {
			for _, source := range []struct{ name, text string }{
				{"physical string", `Справочник.Локальные КАК Л`},
				{"physical reference", `Справочник.Организации КАК О`},
				{"derived output", `(ВЫБРАТЬ "Локально" КАК Филиал) КАК П`},
				{"first union output", `(ВЫБРАТЬ "Локально" КАК Филиал ОБЪЕДИНИТЬ ВЫБРАТЬ "Локально" КАК Один) КАК П`},
				{"proven star", `(ВЫБРАТЬ Л.* ИЗ Справочник.Локальные КАК Л) КАК П`},
			} {
				t.Run("shadow/"+clause+"/"+source.name, func(t *testing.T) {
					child := `ВЫБРАТЬ 1 ИЗ ` + source.text + ` ` + clause + ` Филиал`
					local := opts
					if clause == "ИМЕЮЩИЕ" {
						child = `ВЫБРАТЬ КОЛИЧЕСТВО(1) ИЗ ` + source.text + ` СГРУППИРОВАТЬ ПО Филиал ИМЕЮЩИЕ Филиал = &Значение`
						value := any("Локально")
						if source.name == "physical reference" {
							value = division
							child = `ВЫБРАТЬ КОЛИЧЕСТВО(1) ИЗ ` + source.text + ` СГРУППИРОВАТЬ ПО О.Ссылка, Филиал ИМЕЮЩИЕ Филиал = &Значение`
						}
						local.Params = map[string]any{"Значение": value}
					}
					res := run(t, outer(child), local, []string{"С-А", "С-Б", "С-В", "С-Г"})
					for _, s := range res.Sources {
						if s.Name == "Филиалы" {
							t.Fatalf("local field exposed outer target: %v", res.Sources)
						}
					}
				})
			}
			t.Run("nearest owner/"+clause, func(t *testing.T) {
				child := `ВЫБРАТЬ 1 ` + clause + ` Филиал`
				local := opts
				if clause == "ИМЕЮЩИЕ" {
					child = `ВЫБРАТЬ КОЛИЧЕСТВО(1) ИМЕЮЩИЕ Филиал = &Раздел`
					local.Params = map[string]any{"Раздел": division}
				}
				res := run(t, outer(`ВЫБРАТЬ (`+child+`) ИЗ Справочник.Организации КАК О`), local, []string{"С-А", "С-Б", "С-В", "С-Г"})
				for _, source := range res.Sources {
					if source.Name == "Филиалы" {
						t.Fatalf("nearest owner replaced by grandparent: %v", res.Sources)
					}
				}
			})

			for _, source := range []string{
				`(ВЫБРАТЬ СУММА(1)) КАК П`,
				`(ВЫБРАТЬ 1 КАК Один) КАК Филиал`,
				`Справочник.Разделы КАК Филиал`,
				`Справочник.Разделы КАК Р`,
			} {
				t.Run("fence/"+clause+"/"+source, func(t *testing.T) {
					child := `ВЫБРАТЬ 1 ИЗ ` + source + ` ` + clause + ` Филиал`
					if clause == "ИМЕЮЩИЕ" {
						child = `ВЫБРАТЬ КОЛИЧЕСТВО(1) ИЗ ` + source + ` ИМЕЮЩИЕ Филиал = &Филиал`
					}
					local := opts
					local.Params = map[string]any{"Филиал": first}
					res, err := query.Compile(outer(child), local)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(res.SQL, "ref_филиал") || strings.Contains(res.SQL, "с.филиал_id") {
						t.Fatalf("unproven/local source exposed outer reference: %s", res.SQL)
					}
					// PostgreSQL can sort/group a source alias as a composite.
					// A local value or a refusal is valid, but no outer reference.
					_, _, _ = query.Run(ctx, db, &res)
				})
			}
		}
		for _, clause := range []string{"СГРУППИРОВАТЬ ПО", "УПОРЯДОЧИТЬ ПО", "ИМЕЮЩИЕ"} {
			t.Run("union policies/"+clause, func(t *testing.T) {
				child := `ВЫБРАТЬ 1 ` + clause + ` Филиал`
				orgChild := `ВЫБРАТЬ 1 ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П ` + clause + ` Филиал`
				local := filtered
				want := []string{"О-А", "С-А", "С-Б", "С-Г"}
				if clause == "ИМЕЮЩИЕ" {
					child = `ВЫБРАТЬ КОЛИЧЕСТВО(1) ИМЕЮЩИЕ Филиал = &Филиал`
					orgChild = `ВЫБРАТЬ КОЛИЧЕСТВО(1) ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П ИМЕЮЩИЕ Филиал = &Раздел`
					local.Params = map[string]any{"Филиал": first, "Раздел": division}
					want = []string{"О-А", "С-А"}
				}
				text := `ВЫБРАТЬ С.Наименование КАК Имя ИЗ Справочник.Склады КАК С ГДЕ 1 = (` + child + `) ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ С.Наименование КАК ДругоеИмя ИЗ Справочник.Организации КАК С ГДЕ 1 = (` + orgChild + `) УПОРЯДОЧИТЬ ПО Имя`
				res := run(t, text, local, want)
				for _, e := range ents[:4] {
					found := false
					for _, s := range res.Sources {
						found = found || s.Name == e.Name && s.Kind == "catalog"
					}
					if !found {
						t.Fatalf("missing RBAC source %s: %v", e.Name, res.Sources)
					}
				}
			})
		}
	})
}
