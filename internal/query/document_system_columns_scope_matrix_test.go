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

// Проверяем три границы разрешения системных полей через публичные Compile/Run:
// SELECT-алиасы, автоматические JOIN и проекции производных таблиц (PR #1518).
func TestDocumentSystemColumnsScopeMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entities := documentSystemColumnEntities()
		sale, catalog, special := entities[0], entities[1], entities[2]
		order := &metadata.Entity{Name: "ЗаказСК", Kind: metadata.KindDocument,
			Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}}}
		entities = append(entities, order)
		sale.Fields = append(sale.Fields,
			metadata.Field{Name: "Клиент", Type: "reference:КлиентСК", RefEntity: "КлиентСК"},
			metadata.Field{Name: "Заказ", Type: "reference:ЗаказСК", RefEntity: "ЗаказСК"},
		)
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		clientID, orderID := uuid.New(), uuid.New()
		if err := db.Upsert(ctx, catalog.Name, clientID, map[string]any{"Наименование": "Клиент"}, catalog); err != nil {
			t.Fatal(err)
		}
		if err := db.MarkForDeletion(ctx, catalog.Name, clientID, true); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, order.Name, orderID, map[string]any{"Номер": "Заказ"}, order); err != nil {
			t.Fatal(err)
		}
		for _, item := range []struct {
			number string
			posted bool
		}{
			{"П-1", true},
			{"Ч-1", false},
		} {
			id := uuid.New()
			if err := db.Upsert(ctx, sale.Name, id, map[string]any{
				"Номер": item.number, "Клиент": clientID, "Заказ": orderID,
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
		if err := db.Upsert(ctx, special.Name, uuid.New(), map[string]any{
			"Номер": "О-1", "Проведен": "вручную",
		}, special); err != nil {
			t.Fatal(err)
		}

		for _, tc := range []struct {
			name string
			src  string
			want []string
		}{
			{
				name: "qualified field ignores output alias",
				src: `ВЫБРАТЬ Д.Номер КАК Метка, Д.Проведен КАК Проведен
					ИЗ Документ.ПродажаСК КАК Д ГДЕ Д.Проведен = Истина`,
				want: []string{"П-1"},
			},
			{
				name: "where uses input field",
				src: `ВЫБРАТЬ Номер КАК Метка, 0 КАК Проведен
					ИЗ Документ.ПродажаСК ГДЕ Проведен = Истина`,
				want: []string{"П-1"},
			},
			{
				name: "nested where ignores outer alias",
				src: `ВЫБРАТЬ Номер КАК Метка, 1 КАК Проведен ИЗ Документ.ПродажаСК
					ГДЕ Ссылка В (ВЫБРАТЬ Ссылка ИЗ Документ.ПродажаСК ГДЕ Проведен = Истина)`,
				want: []string{"П-1"},
			},
			{
				name: "union branches have separate aliases",
				src: `ВЫБРАТЬ Номер КАК Метка, Проведен КАК Проведен
					ИЗ Документ.ПродажаСК ГДЕ Проведен = Ложь
					ОБЪЕДИНИТЬ ВСЕ
					ВЫБРАТЬ Д.Номер, Д.Проведен ИЗ Документ.ПродажаСК КАК Д ГДЕ Д.Проведен = Истина
					УПОРЯДОЧИТЬ ПО Метка`,
				want: []string{"П-1", "Ч-1"},
			},
			{
				name: "union order uses first branch alias",
				src: `ВЫБРАТЬ Номер КАК Метка, Номер КАК Проведен
					ИЗ Документ.ПродажаСК ГДЕ Проведен = Ложь
					ОБЪЕДИНИТЬ ВСЕ
					ВЫБРАТЬ Номер, Номер ИЗ Документ.ПродажаСК ГДЕ Проведен = Истина
					УПОРЯДОЧИТЬ ПО Проведен`,
				want: []string{"П-1", "Ч-1"},
			},
			{
				name: "order uses current output alias",
				src: `ВЫБРАТЬ Номер КАК Метка, Номер КАК Проведен
					ИЗ Документ.ПродажаСК УПОРЯДОЧИТЬ ПО Проведен`,
				want: []string{"П-1", "Ч-1"},
			},
			{
				name: "qualified order uses input field",
				src: `ВЫБРАТЬ Д.Номер КАК Метка, Д.Номер КАК Проведен
					ИЗ Документ.ПродажаСК КАК Д УПОРЯДОЧИТЬ ПО Д.Проведен`,
				want: []string{"Ч-1", "П-1"},
			},
			{
				name: "having uses grouped input field",
				src: `ВЫБРАТЬ МИНИМУМ(Номер) КАК Метка, Проведен КАК Проведен
					ИЗ Документ.ПродажаСК СГРУППИРОВАТЬ ПО Проведен ИМЕЮЩИЕ Проведен = Истина`,
				want: []string{"П-1"},
			},
			{
				name: "deletion mark with automatic catalog join",
				src: `ВЫБРАТЬ Номер КАК Метка, Клиент
					ИЗ Документ.ПродажаСК ГДЕ ПометкаУдаления = Ложь`,
				want: []string{"П-1"},
			},
			{
				name: "posted with automatic document join",
				src: `ВЫБРАТЬ Номер КАК Метка, Заказ
					ИЗ Документ.ПродажаСК КАК Д ГДЕ Проведен = Истина`,
				want: []string{"П-1"},
			},
			{
				name: "nested scope with outer automatic join",
				src: `ВЫБРАТЬ Номер КАК Метка, Клиент ИЗ Документ.ПродажаСК КАК Д
					ГДЕ Ссылка В (ВЫБРАТЬ Ссылка ИЗ Документ.ПродажаСК КАК П ГДЕ Проведен = Истина)`,
				want: []string{"П-1"},
			},
			{
				name: "derived posted and deletion mark",
				src: `ВЫБРАТЬ Т.Номер КАК Метка, Т.Проведен, Т.ПометкаУдаления
					ИЗ (ВЫБРАТЬ Номер, Проведен, ПометкаУдаления ИЗ Документ.ПродажаСК) КАК Т
					ГДЕ Т.Проведен = Истина И Т.ПометкаУдаления = Ложь`,
				want: []string{"П-1"},
			},
			{
				name: "bare derived field",
				src: `ВЫБРАТЬ Номер КАК Метка
					ИЗ (ВЫБРАТЬ Номер, Проведен ИЗ Документ.ПродажаСК) КАК Т ГДЕ Проведен = Истина`,
				want: []string{"П-1"},
			},
			{
				name: "nested derived scopes",
				src: `ВЫБРАТЬ П.Номер КАК Метка
					ИЗ (ВЫБРАТЬ Т.Номер, Т.Проведен
						ИЗ (ВЫБРАТЬ Номер, (Проведен) ИЗ Документ.ПродажаСК) КАК Т) КАК П
					ГДЕ П.Проведен = Истина`,
				want: []string{"П-1"},
			},
			{
				name: "derived wildcard",
				src: `ВЫБРАТЬ Т.Номер КАК Метка ИЗ (ВЫБРАТЬ * ИЗ Документ.ПродажаСК) КАК Т
					ГДЕ Т.Проведен = Истина И Т.ПометкаУдаления = Ложь`,
				want: []string{"П-1"},
			},
			{
				name: "derived qualified wildcard",
				src: `ВЫБРАТЬ Т.Номер КАК Метка ИЗ (ВЫБРАТЬ Д.* ИЗ Документ.ПродажаСК КАК Д) КАК Т
					ГДЕ Т.Проведен = Истина`,
				want: []string{"П-1"},
			},
			{
				name: "derived explicit system alias",
				src: `ВЫБРАТЬ Т.Номер КАК Метка, Т.Проведен
					ИЗ (ВЫБРАТЬ Номер, Проведен КАК Проведен ИЗ Документ.ПродажаСК) КАК Т
					ГДЕ Т.Проведен = Истина`,
				want: []string{"П-1"},
			},
			{
				name: "derived explicit user alias",
				src: `ВЫБРАТЬ Т.Проведен КАК Метка
					ИЗ (ВЫБРАТЬ Номер КАК Проведен ИЗ Документ.ПродажаСК) КАК Т
					ГДЕ Т.Проведен = "Ч-1"`,
				want: []string{"Ч-1"},
			},
			{
				name: "derived explicit alias beside system column",
				src: `ВЫБРАТЬ Т.Проведен КАК Метка
					ИЗ (ВЫБРАТЬ Проведен, Номер КАК Проведен ИЗ Документ.ПродажаСК) КАК Т
					ГДЕ Т.Проведен = "Ч-1"`,
				want: []string{"Ч-1"},
			},
			{
				name: "derived own field keeps precedence",
				src: `ВЫБРАТЬ Т.Проведен КАК Метка
					ИЗ (ВЫБРАТЬ Проведен ИЗ Документ.ОсобыйСК) КАК Т`,
				want: []string{"вручную"},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				res, err := query.Compile(tc.src, query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
				if err != nil {
					t.Fatalf("Compile: %v", err)
				}
				rows, _, err := query.Run(ctx, db, &res)
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
