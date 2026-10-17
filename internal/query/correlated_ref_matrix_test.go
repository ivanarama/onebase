package query_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// #1882: коррелированная навигация использует JOIN именно внешнего источника.
// Внутренний источник с тем же именем ссылки может иметь другую цель; его
// alias, в том числе alias производной таблицы, затеняет внешний источник.
func TestCorrelatedRefNavigationMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		customer := &metadata.Entity{Name: "КлиентПроба", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
		other := &metadata.Entity{Name: "КонтрагентПроба", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
		order := &metadata.Entity{Name: "ЗаказПроба", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Клиент", Type: "reference:КлиентПроба", RefEntity: customer.Name},
		}}
		inner := &metadata.Entity{Name: "ВнутренняяПроба", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Клиент", Type: "reference:КонтрагентПроба", RefEntity: other.Name},
		}}
		entities := []*metadata.Entity{customer, other, order, inner}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		live, deleted, otherID := uuid.New(), uuid.New(), uuid.New()
		for _, row := range []struct {
			ent  *metadata.Entity
			id   uuid.UUID
			data map[string]any
		}{
			{customer, live, map[string]any{"Наименование": "Живой"}},
			{customer, deleted, map[string]any{"Наименование": "Удалённый"}},
			{other, otherID, map[string]any{"Наименование": "Другой"}},
			{order, uuid.New(), map[string]any{"Номер": "001", "Клиент": live}},
			{order, uuid.New(), map[string]any{"Номер": "002", "Клиент": deleted}},
			{inner, uuid.New(), map[string]any{"Клиент": otherID}},
		} {
			if err := db.Upsert(ctx, row.ent.Name, row.id, row.data, row.ent); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.MarkForDeletion(ctx, customer.Name, deleted, true); err != nil {
			t.Fatal(err)
		}
		if err := db.MarkForDeletion(ctx, other.Name, otherID, true); err != nil {
			t.Fatal(err)
		}
		compile := func(t *testing.T, src string) query.Result {
			t.Helper()
			r, err := query.Compile(src, query.CompileOpts{Entities: entities, Dialect: db.Dialect(), Params: map[string]any{"Другой": otherID}})
			if err != nil {
				t.Fatalf("compile: %v\nquery: %s", err, src)
			}
			return r
		}
		cases := []struct{ name, src string }{
			{"original", `ВЫБРАТЬ Д.Номер ИЗ Документ.ЗаказПроба КАК Д ГДЕ Д.Ссылка В
    (ВЫБРАТЬ П.Ссылка ИЗ Документ.ЗаказПроба КАК П ГДЕ Д.Клиент.deletion_mark = Ложь)`},
			{"source name", `ВЫБРАТЬ ЗаказПроба.Номер ИЗ Документ.ЗаказПроба ГДЕ ЗаказПроба.Ссылка В
    (ВЫБРАТЬ ЗаказПроба.Ссылка ИЗ Справочник.ВнутренняяПроба КАК П ГДЕ ЗаказПроба.Клиент.deletion_mark = Ложь)`},
			{"grandparent", `ВЫБРАТЬ Д.Номер ИЗ Документ.ЗаказПроба КАК Д ГДЕ Д.Ссылка В
    (ВЫБРАТЬ П.Ссылка ИЗ Документ.ЗаказПроба КАК П ГДЕ П.Ссылка В
     (ВЫБРАТЬ К.Ссылка ИЗ Документ.ЗаказПроба КАК К ГДЕ Д.Клиент.deletion_mark = Ложь))`},
			{"different reference targets", `ВЫБРАТЬ Д.Номер ИЗ Документ.ЗаказПроба КАК Д ГДЕ Д.Ссылка В
    (ВЫБРАТЬ Д.Ссылка ИЗ Справочник.ВнутренняяПроба КАК П
     ГДЕ Д.Клиент.deletion_mark = Ложь И П.Клиент = &Другой)`},
			{"union children", `ВЫБРАТЬ Д.Номер ИЗ Документ.ЗаказПроба КАК Д ГДЕ Д.Ссылка В
    (ВЫБРАТЬ П.Ссылка ИЗ Документ.ЗаказПроба КАК П ГДЕ Д.Клиент.deletion_mark = Ложь
     ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ К.Ссылка ИЗ Документ.ЗаказПроба КАК К ГДЕ Д.Клиент.deletion_mark = Ложь)`},
			{"reference attribute", `ВЫБРАТЬ Д.Номер ИЗ Документ.ЗаказПроба КАК Д ГДЕ Д.Ссылка В
    (ВЫБРАТЬ П.Ссылка ИЗ Документ.ЗаказПроба КАК П ГДЕ Д.Клиент.Наименование = "Живой")`},
			// The explicit alias remains valid; the generated outer JOIN now
			// receives another name, so correlation still reads the live customer.
			{"join alias collision", `ВЫБРАТЬ Д.Номер ИЗ Документ.ЗаказПроба КАК Д ГДЕ Д.Ссылка В
    (ВЫБРАТЬ Д.Ссылка ИЗ Справочник.ВнутренняяПроба КАК ref_клиент ГДЕ Д.Клиент.deletion_mark = Ложь)`},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				r := compile(t, tc.src)
				rows, _, err := query.Run(ctx, db, &r)
				if err != nil {
					t.Fatalf("run: %v\nSQL: %s", err, r.SQL)
				}
				if len(rows) != 1 || rows[0]["номер"] != "001" {
					t.Fatalf("rows: %#v\nSQL: %s", rows, r.SQL)
				}
				found := false
				for _, source := range r.Sources {
					if source.Kind == "catalog" && source.Name == customer.Name {
						found = true
					}
				}
				if !found {
					t.Fatalf("reference target missing from permission sources: %#v", r.Sources)
				}
			})
		}
		// Эти пути не имеют собственного авто-JOIN. Нельзя молча подменить их
		// внешним ref_клиент: запрос должен завершиться отказом, а не чужими данными.
		for _, tc := range []struct{ name, src string }{
			{"unknown qualifier", `ВЫБРАТЬ Д.Номер ИЗ Документ.ЗаказПроба КАК Д ГДЕ Д.Ссылка В
    (ВЫБРАТЬ П.Ссылка ИЗ Документ.ЗаказПроба КАК П ГДЕ Чужой.Клиент.deletion_mark = Ложь)`},
			{"shadow with different target", `ВЫБРАТЬ Д.Номер ИЗ Документ.ЗаказПроба КАК Д ГДЕ Д.Ссылка В
    (ВЫБРАТЬ Д.Ссылка ИЗ Справочник.ВнутренняяПроба КАК Д ГДЕ Д.Клиент.deletion_mark = Ложь)`},
			{"shadow without reference", `ВЫБРАТЬ Д.Номер ИЗ Документ.ЗаказПроба КАК Д ГДЕ Д.Ссылка В
    (ВЫБРАТЬ Д.Ссылка ИЗ Справочник.КонтрагентПроба КАК Д ГДЕ Д.Клиент.deletion_mark = Ложь)`},
			{"derived shadow", `ВЫБРАТЬ Д.Номер ИЗ Документ.ЗаказПроба КАК Д ГДЕ Д.Ссылка В
    (ВЫБРАТЬ Д.Ссылка ИЗ (ВЫБРАТЬ П.Ссылка, П.Клиент ИЗ Справочник.ВнутренняяПроба КАК П) КАК Д
     ГДЕ Д.Клиент.deletion_mark = Ложь)`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				r, err := query.Compile(tc.src, query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
				if err != nil {
					if !strings.Contains(err.Error(), "нет ссылочного реквизита") {
						t.Fatal(err)
					}
					return
				}
				if rows, _, err := query.Run(ctx, db, &r); err == nil {
					t.Fatalf("invalid path returned rows: %#v\nSQL: %s", rows, r.SQL)
				}
			})
		}
		// #1385 разрешает незатенённую корреляцию и к явному JOIN внешнего
		// SELECT. Этот сценарий прежде был в списке неподдержанных путей.
		t.Run("joined source in outer scope", func(t *testing.T) {
			for _, deleted := range []bool{false, true} {
				value := "Ложь"
				wantRows := 0
				if deleted {
					value = "Истина"
					wantRows = 2
				}
				r := compile(t, `ВЫБРАТЬ Д.Номер ИЗ Документ.ЗаказПроба КАК Д
                    ЛЕВОЕ СОЕДИНЕНИЕ Справочник.ВнутренняяПроба КАК П ПО 1 = 1
                    ГДЕ Д.Ссылка В (ВЫБРАТЬ К.Ссылка ИЗ Документ.ЗаказПроба КАК К ГДЕ П.Клиент.deletion_mark = `+value+`) УПОРЯДОЧИТЬ ПО Д.Номер`)
				rows, _, err := query.Run(ctx, db, &r)
				if err != nil {
					t.Fatalf("Run: %v\nSQL: %s", err, r.SQL)
				}
				if len(rows) != wantRows || deleted && (rows[0]["номер"] != "001" || rows[1]["номер"] != "002") {
					t.Fatalf("rows: %v", rows)
				}
				found := false
				for _, source := range r.Sources {
					if source == (query.SourceRef{Kind: "catalog", Name: other.Name}) {
						found = true
					}
				}
				if !found {
					t.Fatalf("target missing from RBAC sources: %v", r.Sources)
				}
			}
		})
		t.Run("single hop limit", func(t *testing.T) {
			_, err := query.Compile(`ВЫБРАТЬ Д.Номер ИЗ Документ.ЗаказПроба КАК Д ГДЕ Д.Ссылка В
    (ВЫБРАТЬ П.Ссылка ИЗ Документ.ЗаказПроба КАК П ГДЕ Д.Клиент.Владелец.Наименование = "x")`,
				query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
			if err == nil || !strings.Contains(err.Error(), "на один уровень") {
				t.Fatalf("single hop diagnostic: %v", err)
			}
		})
	})
}
