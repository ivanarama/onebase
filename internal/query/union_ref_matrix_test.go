package query_test

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Exercise public compilation and execution: every SELECT owns its auto-JOIN,
// including sibling and nested UNIONs with different aliases and reference targets.
func TestUnionReferenceNavigationMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := []*metadata.Entity{
			{Name: "Филиалы", Kind: metadata.KindCatalog, Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Owner", Type: metadata.FieldTypeString},
			}},
			{Name: "Разделы", Kind: metadata.KindCatalog, Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Owner", Type: metadata.FieldTypeString},
			}},
			{Name: "Склады", Kind: metadata.KindCatalog, Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Филиал", Type: "reference:Филиалы", RefEntity: "Филиалы"},
				{Name: "Owner", Type: metadata.FieldTypeString},
			}},
			{Name: "Организации", Kind: metadata.KindCatalog, Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Филиал", Type: "reference:Разделы", RefEntity: "Разделы"},
				{Name: "Owner", Type: metadata.FieldTypeString},
			}},
		}
		reg := &metadata.Register{Name: "Запасы", Dimensions: []metadata.Field{
			{Name: "Склад", Type: "reference:Склады", RefEntity: "Склады"},
		}, Resources: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}}}
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatal(err)
		}
		upsert := func(e *metadata.Entity, name, owner string, ref uuid.UUID) uuid.UUID {
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
		branch := upsert(ents[0], "Ф-А", "own", uuid.Nil)
		denied := upsert(ents[0], "Ф-Б", "other", uuid.Nil)
		division := upsert(ents[1], "Р-А", "own", uuid.Nil)
		warehouse := upsert(ents[2], "С-А", "own", branch)
		upsert(ents[2], "С-Б", "other", branch)
		upsert(ents[2], "С-В", "own", denied)
		upsert(ents[3], "О-А", "own", division)
		period := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		if err := db.WriteMovements(ctx, reg.Name, "Документ", uuid.New(), []map[string]any{
			{"ВидДвижения": "Приход", "Склад": warehouse, "Количество": 1},
		}, reg, &period); err != nil {
			t.Fatal(err)
		}
		opts := query.CompileOpts{Entities: ents, Registers: []*metadata.Register{reg}, Dialect: db.Dialect(), Params: map[string]any{"Филиал": branch, "Раздел": division, "ЗакрытыйФилиал": denied}}
		cases := []struct {
			name, text string
			want       []string
			filtered   bool
		}{
			{"union all", `ВЫБРАТЬ С.Филиал.Наименование КАК Имя ИЗ Справочник.Склады КАК С ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Е.Филиал.Наименование ИЗ Справочник.Склады КАК Е`, []string{"Ф-А", "Ф-А", "Ф-А", "Ф-А", "Ф-Б", "Ф-Б"}, false},
			{"distinct", `ВЫБРАТЬ С.Филиал.Наименование КАК Имя ИЗ Справочник.Склады КАК С ОБЪЕДИНИТЬ ВЫБРАТЬ Е.Филиал.Наименование ИЗ Справочник.Склады КАК Е УПОРЯДОЧИТЬ ПО Имя`, []string{"Ф-А", "Ф-Б"}, false},
			{"second branch only", `ВЫБРАТЬ "Начало" КАК Имя ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Филиал.Наименование ИЗ Справочник.Склады`, []string{"Начало", "Ф-А", "Ф-А", "Ф-Б"}, false},
			{"compound reference alias", `ВЫБРАТЬ С.Филиал.Наименование КАК Имя ИЗ Справочник.Склады КАК С ОБЪЕДИНИТЬ ВЫБРАТЬ О.Филиал.Наименование ИЗ Справочник.Организации КАК О УПОРЯДОЧИТЬ ПО Имя`, []string{"Р-А", "Ф-А", "Ф-Б"}, false},
			{"different targets and third branch", `ВЫБРАТЬ С.Филиал.Наименование КАК Имя ИЗ Справочник.Склады КАК С ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ С.Филиал.Наименование ИЗ Справочник.Организации КАК С ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ "Конец"`, []string{"Конец", "Р-А", "Ф-А", "Ф-А", "Ф-Б"}, false},
			{"explicit join in second branch", `ВЫБРАТЬ "Начало" КАК Имя ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ С.Филиал.Наименование ИЗ Справочник.Склады КАК С ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Филиалы КАК Ф ПО Ф.Ссылка = С.Филиал ГДЕ Ф.Наименование = "Ф-Б"`, []string{"Начало", "Ф-Б"}, false},
			{"where navigation", `ВЫБРАТЬ С.Наименование КАК Имя ИЗ Справочник.Склады КАК С ГДЕ С.Филиал.Наименование = "Ф-Б" ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Е.Наименование ИЗ Справочник.Склады КАК Е ГДЕ Е.Филиал.Наименование = "Ф-А"`, []string{"С-А", "С-Б", "С-В"}, false},
			{"nested union", `ВЫБРАТЬ П.Имя ИЗ (ВЫБРАТЬ С.Филиал.Наименование КАК Имя ИЗ Справочник.Склады КАК С ОБЪЕДИНИТЬ ВЫБРАТЬ О.Филиал.Наименование ИЗ Справочник.Организации КАК О) КАК П УПОРЯДОЧИТЬ ПО П.Имя`, []string{"Р-А", "Ф-А", "Ф-Б"}, false},
			{"restore outer context", `ВЫБРАТЬ С.Филиал.Наименование КАК Имя ИЗ Справочник.Склады КАК С ГДЕ С.Филиал.Наименование В (ВЫБРАТЬ Ф.Наименование ИЗ Справочник.Филиалы КАК Ф) И С.Филиал.Наименование = "Ф-Б"`, []string{"Ф-Б"}, false},
			{"reference attribute in register", `ВЫБРАТЬ Д.Склад.Филиал КАК Имя ИЗ РегистрНакопления.Запасы КАК Д ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Е.Склад.Филиал ИЗ РегистрНакопления.Запасы КАК Е`, []string{branch.String(), branch.String()}, false},
			{"virtual table branches", `ВЫБРАТЬ Д.Склад.Наименование КАК Имя ИЗ РегистрНакопления.Запасы.Остатки() КАК Д ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Е.Склад.Наименование ИЗ РегистрНакопления.Запасы.Остатки() КАК Е`, []string{"С-А", "С-А"}, false},
			{"branch and target row policies", `ВЫБРАТЬ С.Филиал.Наименование КАК Имя ИЗ Справочник.Склады КАК С ГДЕ С.Филиал.Наименование <> "" ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ О.Филиал.Наименование ИЗ Справочник.Организации КАК О ГДЕ О.Филиал.Наименование <> ""`, []string{"Р-А", "Ф-А"}, true},
			{"derived correlation row policies", `ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П) КАК Имя ИЗ Справочник.Склады КАК С ГДЕ (ВЫБРАТЬ Филиал.Наименование ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П) <> "" ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П) ИЗ Справочник.Организации КАК О ГДЕ (ВЫБРАТЬ Филиал.Наименование ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П) <> ""`, []string{"Р-А", "Ф-А"}, true},
			{"bare correlated reference row policies", `ВЫБРАТЬ (ВЫБРАТЬ Филиал) КАК Имя ИЗ Справочник.Склады КАК С ГДЕ (ВЫБРАТЬ Филиал) <> "" ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ (ВЫБРАТЬ Филиал) ИЗ Справочник.Организации КАК О ГДЕ (ВЫБРАТЬ Филиал) <> ""`, []string{"Р-А", "Ф-А"}, true},
			{"bare derived reference row policies", `ВЫБРАТЬ (ВЫБРАТЬ Филиал ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П) КАК Имя ИЗ Справочник.Склады КАК С ГДЕ (ВЫБРАТЬ Филиал ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П) <> "" ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ (ВЫБРАТЬ Филиал ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П) ИЗ Справочник.Организации КАК О ГДЕ (ВЫБРАТЬ Филиал ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П) <> ""`, []string{"Р-А", "Ф-А"}, true},
			{"correlated navigation row policies", `ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование) КАК Имя ИЗ Справочник.Склады КАК С ГДЕ (ВЫБРАТЬ Филиал.Наименование) <> "" ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование) ИЗ Справочник.Организации КАК О ГДЕ (ВЫБРАТЬ Филиал.Наименование) <> ""`, []string{"Р-А", "Ф-А"}, true},
			{"nested row policies", `ВЫБРАТЬ П.Имя ИЗ (ВЫБРАТЬ С.Филиал.Наименование КАК Имя ИЗ Справочник.Склады КАК С ГДЕ С.Филиал.Наименование <> "" ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ О.Филиал.Наименование ИЗ Справочник.Организации КАК О ГДЕ О.Филиал.Наименование <> "") КАК П`, []string{"Р-А", "Ф-А"}, true},
			{"bare predicate union owners", `ВЫБРАТЬ С.Наименование КАК Имя ИЗ Справочник.Склады КАК С ГДЕ 1 = (ВЫБРАТЬ 1 ГДЕ Филиал = &Филиал) ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ С.Наименование КАК ДругоеИмя ИЗ Справочник.Организации КАК С ГДЕ 1 = (ВЫБРАТЬ 1 ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П ГДЕ Филиал = &Раздел) УПОРЯДОЧИТЬ ПО Имя`, []string{"О-А", "С-А", "С-Б"}, false},
			{"bare predicate union source policies", `ВЫБРАТЬ С.Наименование КАК Имя ИЗ Справочник.Склады КАК С ГДЕ 1 = (ВЫБРАТЬ 1 ГДЕ Филиал = &Филиал) ОБЪЕДИНИТЬ ВЫБРАТЬ С.Наименование КАК ДругоеИмя ИЗ Справочник.Организации КАК С ГДЕ 1 = (ВЫБРАТЬ (ВЫБРАТЬ 1 ГДЕ Филиал = &Раздел) ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П) УПОРЯДОЧИТЬ ПО Имя`, []string{"О-А", "С-А"}, true},
			{"bare predicate and display target policies", `ВЫБРАТЬ ЕСТЬNULL((ВЫБРАТЬ Филиал), "") КАК Имя ИЗ Справочник.Склады КАК С ГДЕ 1 = (ВЫБРАТЬ 1 ГДЕ Филиал = &ЗакрытыйФилиал) ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ (ВЫБРАТЬ Филиал) ИЗ Справочник.Организации КАК О ГДЕ 1 = (ВЫБРАТЬ 1 ГДЕ Филиал = &Раздел) УПОРЯДОЧИТЬ ПО Имя`, []string{"", "Р-А"}, true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				localOpts := opts
				if tc.filtered {
					localOpts.RowFilters = map[query.SourceRef]*storage.Predicate{}
					for _, e := range ents {
						localOpts.RowFilters[query.SourceRef{Kind: "catalog", Name: e.Name}] = &storage.Predicate{Field: "Owner", Op: "eq", Value: "own"}
					}
				}
				res, err := query.Compile(tc.text, localOpts)
				if err != nil {
					t.Fatal(err)
				}
				rows, cols, err := query.Run(ctx, db, &res)
				if err != nil {
					t.Fatalf("run: %v\nSQL: %s", err, res.SQL)
				}
				if len(cols) != 1 || cols[0] != "имя" {
					t.Fatalf("columns = %v; want [имя]", cols)
				}
				var got []string
				for _, row := range rows {
					got = append(got, fmt.Sprint(row["имя"]))
				}
				sort.Strings(got)
				want := append([]string(nil), tc.want...)
				sort.Strings(want)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("rows = %v; want %v\nSQL: %s", got, want, res.SQL)
				}
				if tc.filtered {
					for _, name := range []string{"Филиалы", "Разделы", "Склады", "Организации"} {
						found := false
						for _, source := range res.Sources {
							if source.Kind == "catalog" && source.Name == name {
								found = true
							}
						}
						if !found {
							t.Errorf("missing RBAC source %s: %v", name, res.Sources)
						}
					}
				}
			})
		}
	})
}
