package query_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestUnionReferenceReviewMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := []*metadata.Entity{
			{Name: "Филиалы", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}},
			{Name: "Разделы", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}},
			{Name: "Склады", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Филиал", Type: "reference:Филиалы", RefEntity: "Филиалы"}}},
			{Name: "Организации", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Филиал", Type: "reference:Разделы", RefEntity: "Разделы"}}},
		}
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatal(err)
		}
		insert := func(e *metadata.Entity, name string, ref uuid.UUID) uuid.UUID {
			t.Helper()
			id := uuid.New()
			values := map[string]any{"Наименование": name}
			if ref != uuid.Nil {
				values["Филиал"] = ref
			}
			if err := db.Upsert(ctx, e.Name, id, values, e); err != nil {
				t.Fatal(err)
			}
			return id
		}
		branch := insert(ents[0], "Ф-А", uuid.Nil)
		other := insert(ents[0], "Ф-Б", uuid.Nil)
		division := insert(ents[1], "Р-А", uuid.Nil)
		insert(ents[2], "С-А", branch)
		insert(ents[2], "С-Б", other)
		insert(ents[3], "О-А", division)
		cases := []struct {
			name, text, column string
			want               []string
		}{
			{"correlated reference", `ВЫБРАТЬ С.Наименование КАК Имя ИЗ Справочник.Склады КАК С ГДЕ "Ф-А" В (ВЫБРАТЬ Ф.Наименование ИЗ Справочник.Филиалы КАК Ф ГДЕ Ф.Ссылка = С.Филиал)`, "имя", []string{"С-А"}},
			{"two outer levels", `ВЫБРАТЬ С.Наименование КАК Имя ИЗ Справочник.Склады КАК С ГДЕ "Ф-А" В (ВЫБРАТЬ Ф.Наименование ИЗ Справочник.Филиалы КАК Ф ГДЕ Ф.Ссылка В (ВЫБРАТЬ Е.Ссылка ИЗ Справочник.Филиалы КАК Е ГДЕ Е.Ссылка = С.Филиал))`, "имя", []string{"С-А"}},
			{"correlation in both union siblings", `ВЫБРАТЬ С.Наименование КАК Имя ИЗ Справочник.Склады КАК С ГДЕ "Ф-А" В (ВЫБРАТЬ Ф.Наименование ИЗ Справочник.Филиалы КАК Ф ГДЕ Ф.Ссылка = С.Филиал) ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ С.Наименование ИЗ Справочник.Организации КАК С ГДЕ "Р-А" В (ВЫБРАТЬ Ф.Наименование ИЗ Справочник.Разделы КАК Ф ГДЕ Ф.Ссылка = С.Филиал)`, "имя", []string{"С-А", "О-А"}},
			{"local qualifier shadows outer", `ВЫБРАТЬ С.Наименование КАК Имя ИЗ Справочник.Склады КАК С ГДЕ "Р-А" В (ВЫБРАТЬ Ф.Наименование ИЗ Справочник.Разделы КАК Ф ГДЕ Ф.Ссылка В (ВЫБРАТЬ С.Филиал ИЗ Справочник.Организации КАК С)) УПОРЯДОЧИТЬ ПО Имя`, "имя", []string{"С-А", "С-Б"}},
			{"union order alias collides with own field", `ВЫБРАТЬ С.Наименование КАК Наименование ИЗ Справочник.Склады КАК С ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Е.Наименование ИЗ Справочник.Склады КАК Е ГДЕ Е.Филиал = &Филиал УПОРЯДОЧИТЬ ПО Наименование`, "наименование", []string{"С-А", "С-А", "С-Б"}},
			{"distinct order descending", `ВЫБРАТЬ С.Наименование КАК Наименование ИЗ Справочник.Склады КАК С ОБЪЕДИНИТЬ ВЫБРАТЬ Е.Наименование ИЗ Справочник.Склады КАК Е ГДЕ Е.Филиал = &Филиал УПОРЯДОЧИТЬ ПО Наименование УБЫВ`, "наименование", []string{"С-Б", "С-А"}},
			{"order alias collides with reference", `ВЫБРАТЬ С.Наименование КАК Филиал ИЗ Справочник.Склады КАК С ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Е.Наименование ИЗ Справочник.Склады КАК Е ГДЕ Е.Филиал = &Филиал УПОРЯДОЧИТЬ ПО Филиал`, "филиал", []string{"С-А", "С-А", "С-Б"}},
			{"union output alias named boolean literal", `ВЫБРАТЬ С.Наименование КАК Истина ИЗ Справочник.Склады КАК С ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Е.Наименование ИЗ Справочник.Склады КАК Е ГДЕ Е.Филиал = &Филиал УПОРЯДОЧИТЬ ПО Истина`, "истина", []string{"С-А", "С-А", "С-Б"}},
			{"third branch alias", `ВЫБРАТЬ С.Наименование КАК Наименование ИЗ Справочник.Склады КАК С ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Е.Наименование ИЗ Справочник.Склады КАК Е ГДЕ Е.Филиал = &Филиал ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ О.Наименование ИЗ Справочник.Организации КАК О ГДЕ О.Филиал = &Раздел УПОРЯДОЧИТЬ ПО Наименование`, "наименование", []string{"О-А", "С-А", "С-А", "С-Б"}},
			{"nested union order alias", `ВЫБРАТЬ П.Наименование КАК Имя ИЗ (ВЫБРАТЬ С.Наименование КАК Наименование ИЗ Справочник.Склады КАК С ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Е.Наименование ИЗ Справочник.Склады КАК Е ГДЕ Е.Филиал = &Филиал УПОРЯДОЧИТЬ ПО Наименование) КАК П УПОРЯДОЧИТЬ ПО Имя`, "имя", []string{"С-А", "С-А", "С-Б"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				res, err := query.Compile(tc.text, query.CompileOpts{Entities: ents, Dialect: db.Dialect(), Params: map[string]any{"Филиал": branch, "Раздел": division}})
				if err != nil {
					t.Fatal(err)
				}
				rows, cols, err := query.Run(ctx, db, &res)
				if err != nil {
					t.Fatalf("run: %v\nSQL: %s", err, res.SQL)
				}
				if !reflect.DeepEqual(cols, []string{tc.column}) {
					t.Fatalf("columns=%v want %s", cols, tc.column)
				}
				var got []string
				for _, row := range rows {
					got = append(got, fmt.Sprint(row[tc.column]))
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("rows=%v want %v\nSQL: %s", got, tc.want, res.SQL)
				}
			})
		}
	})
}
