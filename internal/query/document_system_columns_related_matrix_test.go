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

// Системное поле принадлежит источнику квалификатора: внешнему SELECT либо
// цели уже поддержанного перехода по ссылке. Проверяем фактическую выдачу,
// включая затенение источника и собственные поля с системными именами.
func TestDocumentSystemColumnsRelatedSourcesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entities := documentSystemColumnEntities()
		sale, catalog, special := entities[0], entities[1], entities[2]
		special.Fields = append(special.Fields, metadata.Field{Name: "ПометкаУдаления", Type: metadata.FieldTypeString})
		order := &metadata.Entity{Name: "ЗаказСК", Kind: metadata.KindDocument,
			Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}}}
		ownCatalog := &metadata.Entity{Name: "ОсобыйКлиентСК", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Проведен", Type: metadata.FieldTypeString},
				{Name: "ПометкаУдаления", Type: metadata.FieldTypeString},
			}}
		entities = append(entities, order, ownCatalog)
		for _, ref := range []struct{ field, entity string }{
			{"Клиент", catalog.Name}, {"Заказ", order.Name},
			{"Особый", special.Name}, {"ОсобыйКлиент", ownCatalog.Name},
		} {
			sale.Fields = append(sale.Fields, metadata.Field{
				Name: ref.field, Type: metadata.FieldType("reference:" + ref.entity), RefEntity: ref.entity,
			})
		}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		specialID, ownCatalogID := uuid.New(), uuid.New()
		for _, item := range []struct {
			entity *metadata.Entity
			id     uuid.UUID
			fields map[string]any
		}{
			{special, specialID, map[string]any{"Номер": "Особый", "Проведен": "вручную", "ПометкаУдаления": "сохранить"}},
			{ownCatalog, ownCatalogID, map[string]any{"Наименование": "Особый", "Проведен": "вручную", "ПометкаУдаления": "сохранить"}},
		} {
			if err := db.Upsert(ctx, item.entity.Name, item.id, item.fields, item.entity); err != nil {
				t.Fatal(err)
			}
		}
		for _, item := range []struct {
			number string
			posted bool
		}{
			{"П-1", true}, {"Ч-1", false},
		} {
			id, clientID, orderID := uuid.New(), uuid.New(), uuid.New()
			if err := db.Upsert(ctx, catalog.Name, clientID, map[string]any{"Наименование": item.number}, catalog); err != nil {
				t.Fatal(err)
			}
			if err := db.MarkForDeletion(ctx, catalog.Name, clientID, item.posted); err != nil {
				t.Fatal(err)
			}
			if err := db.Upsert(ctx, order.Name, orderID, map[string]any{"Номер": item.number}, order); err != nil {
				t.Fatal(err)
			}
			if err := db.SetPosted(ctx, order.Name, orderID, !item.posted); err != nil {
				t.Fatal(err)
			}
			if err := db.Upsert(ctx, sale.Name, id, map[string]any{
				"Номер": item.number, "Клиент": clientID, "Заказ": orderID,
				"Особый": specialID, "ОсобыйКлиент": ownCatalogID,
			}, sale); err != nil {
				t.Fatal(err)
			}
			if err := db.SetPosted(ctx, sale.Name, id, item.posted); err != nil {
				t.Fatal(err)
			}
			if err := db.MarkForDeletion(ctx, sale.Name, id, !item.posted); err != nil {
				t.Fatal(err)
			}
		}

		for _, tc := range []struct {
			name    string
			src     string
			want    []string
			wantErr bool
		}{
			{
				name: "correlated posted",
				src: `ВЫБРАТЬ Д.Номер КАК Метка ИЗ Документ.ПродажаСК КАК Д
					ГДЕ Д.Ссылка В (ВЫБРАТЬ П.Ссылка ИЗ Документ.ПродажаСК КАК П ГДЕ Д.Проведен = Истина)`,
				want: []string{"П-1"},
			},
			{
				name: "correlated deletion mark",
				src: `ВЫБРАТЬ Д.Номер КАК Метка ИЗ Документ.ПродажаСК КАК Д
					ГДЕ Д.Ссылка В (ВЫБРАТЬ П.Ссылка ИЗ Документ.ПродажаСК КАК П ГДЕ Д.ПометкаУдаления = Истина)`,
				want: []string{"Ч-1"},
			},
			{
				name: "correlated grandparent",
				src: `ВЫБРАТЬ Д.Номер КАК Метка ИЗ Документ.ПродажаСК КАК Д
					ГДЕ Д.Ссылка В (ВЫБРАТЬ П.Ссылка ИЗ Документ.ПродажаСК КАК П
					ГДЕ П.Ссылка В (ВЫБРАТЬ Вн.Ссылка ИЗ Документ.ПродажаСК КАК Вн
					ГДЕ Д.Проведен = Истина И Д.ПометкаУдаления = Ложь))`,
				want: []string{"П-1"},
			},
			{
				name: "correlated union branches share parent",
				src: `ВЫБРАТЬ Д.Номер КАК Метка ИЗ Документ.ПродажаСК КАК Д
					ГДЕ Д.Ссылка В (ВЫБРАТЬ П.Ссылка ИЗ Документ.ПродажаСК КАК П ГДЕ Д.Проведен = Истина
					ОБЪЕДИНИТЬ ВЫБРАТЬ Вн.Ссылка ИЗ Документ.ПродажаСК КАК Вн ГДЕ Д.ПометкаУдаления = Ложь)`,
				want: []string{"П-1"},
			},
			{
				name: "correlated derived source",
				src: `ВЫБРАТЬ Д.Номер КАК Метка
					ИЗ (ВЫБРАТЬ Ссылка, Номер, Проведен, ПометкаУдаления ИЗ Документ.ПродажаСК) КАК Д
					ГДЕ Д.Ссылка В (ВЫБРАТЬ П.Ссылка ИЗ Документ.ПродажаСК КАК П
					ГДЕ Д.Проведен = Истина И Д.ПометкаУдаления = Ложь)`,
				want: []string{"П-1"},
			},
			{
				name: "local own fields shadow outer source",
				src: `ВЫБРАТЬ Д.Номер КАК Метка ИЗ Документ.ПродажаСК КАК Д
					ГДЕ "Особый" В (ВЫБРАТЬ Д.Номер ИЗ Документ.ОсобыйСК КАК Д
					ГДЕ Д.Проведен = "вручную" И Д.ПометкаУдаления = "сохранить") УПОРЯДОЧИТЬ ПО Метка`,
				want: []string{"П-1", "Ч-1"},
			},
			{
				name: "nearest parent shadows grandparent",
				src: `ВЫБРАТЬ Д.Номер КАК Метка ИЗ Документ.ПродажаСК КАК Д
					ГДЕ "Особый" В (ВЫБРАТЬ Д.Номер ИЗ Документ.ОсобыйСК КАК Д
					ГДЕ Д.Ссылка В (ВЫБРАТЬ Вн.Ссылка ИЗ Документ.ОсобыйСК КАК Вн ГДЕ Д.Проведен = "вручную"))
					УПОРЯДОЧИТЬ ПО Метка`,
				want: []string{"П-1", "Ч-1"},
			},
			{
				name: "local derived alias shadows outer source",
				src: `ВЫБРАТЬ Д.Номер КАК Метка ИЗ Документ.ПродажаСК КАК Д
					ГДЕ "Особый" В (ВЫБРАТЬ Д.Проведен
					ИЗ (ВЫБРАТЬ Номер КАК Проведен ИЗ Документ.ОсобыйСК) КАК Д) УПОРЯДОЧИТЬ ПО Метка`,
				want: []string{"П-1", "Ч-1"},
			},
			{
				name: "local catalog without posted stops lookup",
				src: `ВЫБРАТЬ Д.Номер КАК Метка ИЗ Документ.ПродажаСК КАК Д
					ГДЕ Д.Номер В (ВЫБРАТЬ Д.Наименование ИЗ Справочник.КлиентСК КАК Д ГДЕ Д.Проведен = Истина)`,
				wantErr: true,
			},
			{
				name: "union sibling is not parent",
				src: `ВЫБРАТЬ Д.Номер КАК Метка ИЗ Документ.ПродажаСК КАК Д
					ОБЪЕДИНИТЬ ВЫБРАТЬ П.Номер ИЗ Документ.ПродажаСК КАК П ГДЕ Д.Проведен = Истина`,
				wantErr: true,
			},
			{
				name: "reference catalog deletion mark",
				src:  `ВЫБРАТЬ Номер КАК Метка ИЗ Документ.ПродажаСК ГДЕ Клиент.ПометкаУдаления = Ложь`,
				want: []string{"Ч-1"},
			},
			{
				name: "reference document posted",
				src:  `ВЫБРАТЬ Номер КАК Метка ИЗ Документ.ПродажаСК ГДЕ Заказ.Проведен = Истина`,
				want: []string{"Ч-1"},
			},
			{
				name: "qualified reference catalog",
				src:  `ВЫБРАТЬ Д.Номер КАК Метка ИЗ Документ.ПродажаСК КАК Д ГДЕ Д.Клиент.ПометкаУдаления = Истина`,
				want: []string{"П-1"},
			},
			{
				name: "qualified reference document",
				src:  `ВЫБРАТЬ Д.Номер КАК Метка ИЗ Документ.ПродажаСК КАК Д ГДЕ Д.Заказ.Проведен = Ложь`,
				want: []string{"П-1"},
			},
			{
				name: "reference projection and output alias",
				src: `ВЫБРАТЬ Номер КАК Метка, Заказ.Проведен КАК Проведен, Клиент.ПометкаУдаления КАК ПометкаУдаления
					ИЗ Документ.ПродажаСК ГДЕ Заказ.Проведен = Истина И Клиент.ПометкаУдаления = Ложь`,
				want: []string{"Ч-1"},
			},
			{
				name: "reference document own fields",
				src: `ВЫБРАТЬ Номер КАК Метка ИЗ Документ.ПродажаСК
					ГДЕ Особый.Проведен = "вручную" И Особый.ПометкаУдаления = "сохранить" УПОРЯДОЧИТЬ ПО Метка`,
				want: []string{"П-1", "Ч-1"},
			},
			{
				name: "reference catalog own fields",
				src: `ВЫБРАТЬ Номер КАК Метка ИЗ Документ.ПродажаСК
					ГДЕ ОсобыйКлиент.Проведен = "вручную" И ОсобыйКлиент.ПометкаУдаления = "сохранить" УПОРЯДОЧИТЬ ПО Метка`,
				want: []string{"П-1", "Ч-1"},
			},
			{
				name:    "reference catalog has no posted flag",
				src:     `ВЫБРАТЬ Номер КАК Метка ИЗ Документ.ПродажаСК ГДЕ Клиент.Проведен = Истина`,
				wantErr: true,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				res, err := query.Compile(tc.src, query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
				if err != nil {
					t.Fatalf("Compile: %v", err)
				}
				rows, _, err := query.Run(ctx, db, &res)
				if tc.wantErr {
					if err == nil {
						t.Fatalf("несуществующее поле разрешилось: %v\nSQL: %s", rows, res.SQL)
					}
					return
				}
				if err != nil {
					t.Fatalf("Run: %v\nSQL: %s", err, res.SQL)
				}
				var got []string
				for _, row := range rows {
					got = append(got, stringOf(row["метка"]))
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("метки = %v, want %v\nSQL: %s", got, tc.want, res.SQL)
				}
			})
		}
	})
}
