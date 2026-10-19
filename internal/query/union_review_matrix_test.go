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

func TestUnionReferenceReviewMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := []*metadata.Entity{
			{Name: "Филиалы", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}},
			{Name: "Разделы", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}},
			{Name: "Склады", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Филиал", Type: "reference:Филиалы", RefEntity: "Филиалы"}}},
			{Name: "Организации", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Филиал", Type: "reference:Разделы", RefEntity: "Разделы"}}},
			{Name: "Локальные", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Филиал", Type: metadata.FieldTypeString}}},
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
		if err := db.Upsert(ctx, ents[4].Name, uuid.New(), map[string]any{"Филиал": "Локально"}, ents[4]); err != nil {
			t.Fatal(err)
		}
		cases := []struct {
			name, text, column string
			want               []string
		}{
			{"bare local field shadows outer", `ВЫБРАТЬ (ВЫБРАТЬ Филиал ИЗ Справочник.Локальные КАК Л) КАК Имя ИЗ Справочник.Склады КАК С`, "имя", []string{"Локально", "Локально"}},
			{"bare local reference shadows outer", `ВЫБРАТЬ (ВЫБРАТЬ Филиал ИЗ Справочник.Организации КАК О) КАК Имя ИЗ Справочник.Склады КАК С`, "имя", []string{"Р-А", "Р-А"}},
			{"bare derived output shadows outer", `ВЫБРАТЬ (ВЫБРАТЬ Филиал ИЗ (ВЫБРАТЬ "Локально" КАК Филиал) КАК П) КАК Имя ИЗ Справочник.Склады КАК С`, "имя", []string{"Локально", "Локально"}},
			{"bare derived star shadows outer", `ВЫБРАТЬ (ВЫБРАТЬ Филиал ИЗ (ВЫБРАТЬ ВСЕ Л.* ИЗ Справочник.Локальные КАК Л) КАК П) КАК Имя ИЗ Справочник.Склады КАК С`, "имя", []string{"Локально", "Локально"}},
			{"bare derived nested star shadows outer", `ВЫБРАТЬ (ВЫБРАТЬ Филиал ИЗ (ВЫБРАТЬ ALL * ИЗ (ВЫБРАТЬ "Локально" КАК Филиал) КАК Е) КАК П) КАК Имя ИЗ Справочник.Склады КАК С`, "имя", []string{"Локально", "Локально"}},
			{"bare projection remains display", `ВЫБРАТЬ Филиал КАК Имя ИЗ Справочник.Склады КАК С УПОРЯДОЧИТЬ ПО Имя`, "имя", []string{"Ф-А", "Ф-Б"}},
			{"bare union all owners", `ВЫБРАТЬ (ВЫБРАТЬ Филиал) КАК Имя ИЗ Справочник.Склады КАК С ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ (ВЫБРАТЬ Филиал) КАК ДругоеИмя ИЗ Справочник.Организации КАК С УПОРЯДОЧИТЬ ПО Имя`, "имя", []string{"Р-А", "Ф-А", "Ф-Б"}},
			{"bare union distinct owners", `ВЫБРАТЬ (ВЫБРАТЬ Филиал) КАК Имя ИЗ Справочник.Склады КАК С ОБЪЕДИНИТЬ ВЫБРАТЬ (ВЫБРАТЬ Филиал) КАК ДругоеИмя ИЗ Справочник.Организации КАК С УПОРЯДОЧИТЬ ПО Имя`, "имя", []string{"Р-А", "Ф-А", "Ф-Б"}},
			{"derived qualifier shadows outer reference", `ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование ИЗ (ВЫБРАТЬ "Локально" КАК Наименование) КАК Филиал) КАК Имя ИЗ Справочник.Склады КАК С УПОРЯДОЧИТЬ ПО Имя`, "имя", []string{"Локально", "Локально"}},
			{"derived bridge qualifier shadows outer reference", `ВЫБРАТЬ (ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование) ИЗ (ВЫБРАТЬ "Локально" КАК Наименование) КАК Филиал) КАК Имя ИЗ Справочник.Склады КАК С УПОРЯДОЧИТЬ ПО Имя`, "имя", []string{"Локально", "Локально"}},
			{"derived child", `ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П) КАК Имя ИЗ Справочник.Склады КАК С УПОРЯДОЧИТЬ ПО Имя`, "имя", []string{"Ф-А", "Ф-Б"}},
			{"derived bridge", `ВЫБРАТЬ (ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование) ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П) КАК Имя ИЗ Справочник.Склады КАК С УПОРЯДОЧИТЬ ПО Имя`, "имя", []string{"Ф-А", "Ф-Б"}},
			{"derived union all siblings", `ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П) КАК Имя ИЗ Справочник.Склады КАК С ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П) КАК ДругоеИмя ИЗ Справочник.Организации КАК С УПОРЯДОЧИТЬ ПО Имя`, "имя", []string{"Р-А", "Ф-А", "Ф-Б"}},
			{"derived union distinct siblings", `ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П) КАК Имя ИЗ Справочник.Склады КАК С ОБЪЕДИНИТЬ ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П) КАК ДругоеИмя ИЗ Справочник.Организации КАК С УПОРЯДОЧИТЬ ПО Имя`, "имя", []string{"Р-А", "Ф-А", "Ф-Б"}},
			{"derived star without matching field", `ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование ИЗ (ВЫБРАТЬ Ф.* ИЗ Справочник.Филиалы КАК Ф ГДЕ Ф.Наименование = "Ф-А") КАК П) КАК Имя ИЗ Справочник.Склады КАК С УПОРЯДОЧИТЬ ПО Имя`, "имя", []string{"Ф-А", "Ф-Б"}},
			{"nested derived star without matching field", `ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование ИЗ (ВЫБРАТЬ * ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П) КАК Е) КАК Имя ИЗ Справочник.Склады КАК С УПОРЯДОЧИТЬ ПО Имя`, "имя", []string{"Ф-А", "Ф-Б"}},
			{"derived union uses first projection names", `ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование ИЗ (ВЫБРАТЬ 1 КАК Один ОБЪЕДИНИТЬ ВЫБРАТЬ 1 КАК Филиал) КАК П) КАК Имя ИЗ Справочник.Склады КАК С УПОРЯДОЧИТЬ ПО Имя`, "имя", []string{"Ф-А", "Ф-Б"}},
			{"correlated navigation without from", `ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование) КАК Имя ИЗ Справочник.Склады КАК С УПОРЯДОЧИТЬ ПО Имя`, "имя", []string{"Ф-А", "Ф-Б"}},
			{"correlated navigation two levels", `ВЫБРАТЬ (ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование)) КАК Имя ИЗ Справочник.Склады КАК С УПОРЯДОЧИТЬ ПО Имя`, "имя", []string{"Ф-А", "Ф-Б"}},
			{"correlated navigation with unrelated child source", `ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование ИЗ Справочник.Филиалы КАК Ф ГДЕ Ф.Наименование = "Ф-А") КАК Имя ИЗ Справочник.Склады КАК С УПОРЯДОЧИТЬ ПО Имя`, "имя", []string{"Ф-А", "Ф-Б"}},
			{"child reference shadows outer navigation", `ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование ИЗ Справочник.Организации КАК О) КАК Имя ИЗ Справочник.Склады КАК С УПОРЯДОЧИТЬ ПО Имя`, "имя", []string{"Р-А", "Р-А"}},
			{"child qualifier shadows outer navigation", `ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование ИЗ Справочник.Разделы КАК Филиал) КАК Имя ИЗ Справочник.Склады КАК С УПОРЯДОЧИТЬ ПО Имя`, "имя", []string{"Р-А", "Р-А"}},
			{"correlated navigation in union siblings", `ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование) КАК Имя ИЗ Справочник.Склады КАК С ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование) ИЗ Справочник.Организации КАК С УПОРЯДОЧИТЬ ПО Имя`, "имя", []string{"Р-А", "Ф-А", "Ф-Б"}},
			{"correlated navigation in child union", `ВЫБРАТЬ С.Наименование КАК Имя ИЗ Справочник.Склады КАК С ГДЕ "Ф-А" В (ВЫБРАТЬ Филиал.Наименование ОБЪЕДИНИТЬ ВЫБРАТЬ Филиал.Наименование)`, "имя", []string{"С-А"}},
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
		t.Run("derived review reproduction", func(t *testing.T) {
			res, err := query.Compile(`ВЫБРАТЬ С.Наименование КАК Имя, (ВЫБРАТЬ Филиал.Наименование ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П) КАК ФилиалИмя ИЗ Справочник.Склады КАК С УПОРЯДОЧИТЬ ПО Имя`, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
			if err != nil {
				t.Fatal(err)
			}
			rows, cols, err := query.Run(ctx, db, &res)
			if err != nil {
				t.Fatalf("run: %v\nSQL: %s", err, res.SQL)
			}
			if !reflect.DeepEqual(cols, []string{"имя", "филиалимя"}) {
				t.Fatalf("columns = %v", cols)
			}
			var got [][2]string
			for _, row := range rows {
				got = append(got, [2]string{fmt.Sprint(row["имя"]), fmt.Sprint(row["филиалимя"])})
			}
			want := [][2]string{{"С-А", "Ф-А"}, {"С-Б", "Ф-Б"}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("rows = %v; want %v\nSQL: %s", got, want, res.SQL)
			}
		})
		t.Run("review reproduction", func(t *testing.T) {
			res, err := query.Compile(`ВЫБРАТЬ С.Наименование КАК Имя, (ВЫБРАТЬ Филиал.Наименование) КАК ФилиалИмя ИЗ Справочник.Склады КАК С УПОРЯДОЧИТЬ ПО Имя`, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
			if err != nil {
				t.Fatal(err)
			}
			rows, cols, err := query.Run(ctx, db, &res)
			if err != nil {
				t.Fatalf("run: %v\nSQL: %s", err, res.SQL)
			}
			if !reflect.DeepEqual(cols, []string{"имя", "филиалимя"}) {
				t.Fatalf("columns = %v", cols)
			}
			var got [][2]string
			for _, row := range rows {
				got = append(got, [2]string{fmt.Sprint(row["имя"]), fmt.Sprint(row["филиалимя"])})
			}
			want := [][2]string{{"С-А", "Ф-А"}, {"С-Б", "Ф-Б"}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("rows = %v; want %v\nSQL: %s", got, want, res.SQL)
			}
		})
		for _, child := range []string{
			`ВЫБРАТЬ Филиал`,
			`ВЫБРАТЬ ВСЕ Филиал`,
			`SELECT ALL Филиал`,
			`ВЫБРАТЬ РАЗЛИЧНЫЕ Филиал`,
			`SELECT DISTINCT Филиал`,
			`ВЫБРАТЬ Филиал ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П`,
			`ВЫБРАТЬ (ВЫБРАТЬ Филиал) ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П`,
			`ВЫБРАТЬ Филиал ИЗ (ВЫБРАТЬ ВСЕ Ф.Наименование ИЗ Справочник.Филиалы КАК Ф ГДЕ Ф.Наименование = "Ф-А") КАК П`,
			`ВЫБРАТЬ Филиал ИЗ (ВЫБРАТЬ ALL Ф.* ИЗ Справочник.Филиалы КАК Ф ГДЕ Ф.Наименование = "Ф-А") КАК П`,
		} {
			t.Run("bare outer reference/"+child, func(t *testing.T) {
				text := `ВЫБРАТЬ С.Наименование КАК Имя, (` + child + `) КАК ФилиалИмя ИЗ Справочник.Склады КАК С УПОРЯДОЧИТЬ ПО Имя`
				res, err := query.Compile(text, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
				if err != nil {
					t.Fatal(err)
				}
				rows, cols, err := query.Run(ctx, db, &res)
				if err != nil {
					t.Fatalf("run: %v\nSQL: %s", err, res.SQL)
				}
				if !reflect.DeepEqual(cols, []string{"имя", "филиалимя"}) {
					t.Fatalf("columns = %v", cols)
				}
				var got [][2]string
				for _, row := range rows {
					got = append(got, [2]string{fmt.Sprint(row["имя"]), fmt.Sprint(row["филиалимя"])})
				}
				want := [][2]string{{"С-А", "Ф-А"}, {"С-Б", "Ф-Б"}}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("rows = %v; want %v\nSQL: %s", got, want, res.SQL)
				}
			})
		}
		for _, child := range []string{
			`ВЫБРАТЬ Филиал ИЗ (ВЫБРАТЬ СУММА(1)) КАК П`,
			`ВЫБРАТЬ Филиал ИЗ (ВЫБРАТЬ 1 КАК Один) КАК Филиал`,
			`ВЫБРАТЬ Филиал ИЗ Справочник.Разделы КАК Филиал`,
			`ВЫБРАТЬ 1 КАК Филиал`,
		} {
			t.Run("bare lookup fence/"+child, func(t *testing.T) {
				res, err := query.Compile(`ВЫБРАТЬ (`+child+`) КАК Имя ИЗ Справочник.Склады КАК С`, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(res.SQL, "ref_филиал") {
					t.Fatalf("declaration or unknown local output exposed outer reference: %s", res.SQL)
				}
				// A source alias can be a composite value on PostgreSQL. Either
				// a local result or a database refusal is valid; outer data is not.
				rows, _, err := query.Run(ctx, db, &res)
				if err == nil {
					for _, row := range rows {
						if got := fmt.Sprint(row["имя"]); got == "Ф-А" || got == "Ф-Б" {
							t.Fatalf("outer reference leaked through local declaration: %v", rows)
						}
					}
				}
			})
		}
		// A reference read only in a child WHERE still denotes its owner's FK.
		for _, child := range []struct{ name, text string }{
			{"without from", `ВЫБРАТЬ 1 ГДЕ %s = &Филиал`},
			{"derived from", `ВЫБРАТЬ 1 ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П ГДЕ %s = &Филиал`},
			{"derived bridge", `ВЫБРАТЬ (ВЫБРАТЬ 1 ГДЕ %s = &Филиал) ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П`},
			{"derived physical column collision", `ВЫБРАТЬ 1 ИЗ (ВЫБРАТЬ "local column" КАК филиал_id) КАК П ГДЕ %s = &Филиал`},
			{"first union output absent", `ВЫБРАТЬ 1 ИЗ (ВЫБРАТЬ 1 КАК Один ОБЪЕДИНИТЬ ВЫБРАТЬ 1 КАК Филиал) КАК П ГДЕ %s = &Филиал`},
			{"proven physical star absent", `ВЫБРАТЬ 1 ИЗ (ВЫБРАТЬ Ф.* ИЗ Справочник.Филиалы КАК Ф ГДЕ Ф.Наименование = "Ф-А") КАК П ГДЕ %s = &Филиал`},
		} {
			for _, field := range []string{"Филиал", "С.Филиал"} {
				for _, param := range []struct {
					name string
					id   uuid.UUID
					want []string
				}{
					{"first", branch, []string{"С-А"}},
					{"second", other, []string{"С-Б"}},
					{"missing", uuid.New(), nil},
				} {
					t.Run("outer reference predicate/"+child.name+"/"+field+"/"+param.name, func(t *testing.T) {
						text := `ВЫБРАТЬ С.Наименование КАК Имя ИЗ Справочник.Склады КАК С ГДЕ 1 = (` + fmt.Sprintf(child.text, field) + `) УПОРЯДОЧИТЬ ПО Имя`
						res, err := query.Compile(text, query.CompileOpts{Entities: ents, Dialect: db.Dialect(), Params: map[string]any{"Филиал": param.id}})
						if err != nil {
							t.Fatal(err)
						}
						rows, cols, err := query.Run(ctx, db, &res)
						if err != nil {
							t.Fatalf("run: %v\nSQL: %s", err, res.SQL)
						}
						if !reflect.DeepEqual(cols, []string{"имя"}) {
							t.Fatalf("columns = %v", cols)
						}
						var got []string
						for _, row := range rows {
							got = append(got, fmt.Sprint(row["имя"]))
						}
						if !reflect.DeepEqual(got, param.want) {
							t.Fatalf("rows = %v; want %v\nSQL: %s", got, param.want, res.SQL)
						}
					})
				}
			}
		}
		// Local data must match independently of each warehouse's outer FK.
		for _, tc := range []struct {
			name, child string
			param       any
		}{
			{"physical string", `ВЫБРАТЬ 1 ИЗ Справочник.Локальные КАК Л ГДЕ Филиал = &Значение`, "Локально"},
			{"physical reference", `ВЫБРАТЬ 1 ИЗ Справочник.Организации КАК О ГДЕ Филиал = &Значение`, division},
			{"nearest parent reference", `ВЫБРАТЬ (ВЫБРАТЬ 1 ГДЕ Филиал = &Значение) ИЗ Справочник.Организации КАК О`, division},
			{"derived field", `ВЫБРАТЬ 1 ИЗ (ВЫБРАТЬ "Локально" КАК Филиал) КАК П ГДЕ Филиал = &Значение`, "Локально"},
			{"first union output", `ВЫБРАТЬ 1 ИЗ (ВЫБРАТЬ 1 КАК Филиал ОБЪЕДИНИТЬ ВЫБРАТЬ 1 КАК Один) КАК П ГДЕ Филиал = &Значение`, 1},
			{"physical star", `ВЫБРАТЬ 1 ИЗ (ВЫБРАТЬ ВСЕ Л.* ИЗ Справочник.Локальные КАК Л) КАК П ГДЕ Филиал = &Значение`, "Локально"},
			{"nested star", `ВЫБРАТЬ (ВЫБРАТЬ 1 ГДЕ Филиал = &Значение) ИЗ (SELECT ALL * ИЗ (ВЫБРАТЬ "Локально" КАК Филиал) КАК Е) КАК П`, "Локально"},
		} {
			t.Run("predicate shadow/"+tc.name, func(t *testing.T) {
				res, err := query.Compile(`ВЫБРАТЬ С.Наименование КАК Имя ИЗ Справочник.Склады КАК С ГДЕ 1 = (`+tc.child+`) УПОРЯДОЧИТЬ ПО Имя`, query.CompileOpts{Entities: ents, Dialect: db.Dialect(), Params: map[string]any{"Значение": tc.param}})
				if err != nil {
					t.Fatal(err)
				}
				rows, cols, err := query.Run(ctx, db, &res)
				if err != nil {
					t.Fatalf("run: %v\nSQL: %s", err, res.SQL)
				}
				if !reflect.DeepEqual(cols, []string{"имя"}) {
					t.Fatalf("columns = %v", cols)
				}
				var got []string
				for _, row := range rows {
					got = append(got, fmt.Sprint(row["имя"]))
				}
				if !reflect.DeepEqual(got, []string{"С-А", "С-Б"}) {
					t.Fatalf("local predicate read outer FK: %v\nSQL: %s", got, res.SQL)
				}
				for _, source := range res.Sources {
					if source.Name == "Филиалы" {
						t.Fatalf("shadowed field exposed outer target: %v", res.Sources)
					}
				}
			})
		}
		for _, child := range []string{
			`ВЫБРАТЬ 1 ИЗ (ВЫБРАТЬ СУММА(1)) КАК П ГДЕ Филиал = &Филиал`,
			`ВЫБРАТЬ 1 ИЗ (ВЫБРАТЬ 1 КАК Один) КАК Филиал ГДЕ Филиал = &Филиал`,
			`ВЫБРАТЬ 1 ИЗ Справочник.Разделы КАК Филиал ГДЕ Филиал = &Филиал`,
			`ВЫБРАТЬ 1 ИЗ Справочник.Разделы КАК Р ГДЕ Филиал = &Филиал`,
		} {
			t.Run("predicate lookup fence/"+child, func(t *testing.T) {
				res, err := query.Compile(`ВЫБРАТЬ С.Наименование КАК Имя ИЗ Справочник.Склады КАК С ГДЕ 1 = (`+child+`)`, query.CompileOpts{Entities: ents, Dialect: db.Dialect(), Params: map[string]any{"Филиал": branch}})
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(res.SQL, "ref_филиал") || strings.Contains(res.SQL, "с.филиал_id") {
					t.Fatalf("lookup fence exposed outer FK: %s", res.SQL)
				}
				if rows, _, err := query.Run(ctx, db, &res); err == nil && len(rows) != 0 {
					t.Fatalf("unproven/local source read outer FK: %v\nSQL: %s", rows, res.SQL)
				}
			})
		}
		// Known local names and unprovable outputs must not read an outer target.
		// Invalid local navigation must remain invalid.
		for _, tc := range []struct{ name, projection string }{
			{"unknown expression output", `ВЫБРАТЬ СУММА(1)`},
			{"explicit alias", `ВЫБРАТЬ 1 КАК Филиал`},
			{"direct field", `ВЫБРАТЬ Л.Филиал ИЗ Справочник.Локальные КАК Л`},
			{"first union output", `ВЫБРАТЬ 1 КАК Филиал ОБЪЕДИНИТЬ ВЫБРАТЬ 1 КАК Один`},
			{"nested star", `ВЫБРАТЬ * ИЗ (ВЫБРАТЬ 1 КАК Филиал) КАК Е`},
			{"nested qualified star", `ВЫБРАТЬ Е.* ИЗ (ВЫБРАТЬ 1 КАК Филиал) КАК Е`},
			{"physical star", `ВЫБРАТЬ * ИЗ Справочник.Локальные КАК Е`},
			{"physical qualified star", `ВЫБРАТЬ Е.* ИЗ Справочник.Локальные КАК Е`},
		} {
			t.Run("derived shadow/"+tc.name, func(t *testing.T) {
				text := `ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование ИЗ (` + tc.projection + `) КАК П) КАК Имя ИЗ Справочник.Склады КАК С`
				res, err := query.Compile(text, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(res.SQL, "ref_филиал") {
					t.Fatalf("local declaration replaced by outer reference: %s", res.SQL)
				}
				if _, _, err := query.Run(ctx, db, &res); err == nil {
					t.Fatalf("scalar navigation unexpectedly succeeded: %s", res.SQL)
				}
			})
		}
		t.Run("derived from with joined local field", func(t *testing.T) {
			res, err := query.Compile(`ВЫБРАТЬ (ВЫБРАТЬ Филиал.Наименование ИЗ (ВЫБРАТЬ 1 КАК Один) КАК П ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Локальные КАК Л ПО Истина) КАК Имя ИЗ Справочник.Склады КАК С`, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(res.SQL, "ref_филиал") {
				t.Fatalf("joined local field replaced by outer reference: %s", res.SQL)
			}
			if _, _, err := query.Run(ctx, db, &res); err == nil {
				t.Fatalf("scalar navigation unexpectedly succeeded: %s", res.SQL)
			}
		})
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
