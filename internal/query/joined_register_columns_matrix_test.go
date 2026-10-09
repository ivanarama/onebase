package query_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Поля присоединённого (не главного) регистра сведений переводятся по его
// метаданным: ссылочное измерение — колонка «_id». Раньше перевод шёл по
// главному источнику, и «ПО М.Склад = О.Склад» падал «no such column: м.склад»
// — а у главной виртуальной таблицы одноимённое измерение ещё и перехватывало
// перевод. Типичный случай — остатки с местом хранения номенклатуры.
func TestJoinedRegisterColumnsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ref := func(name, entity string) metadata.Field {
			return metadata.Field{Name: name, Type: metadata.FieldType("reference:" + entity), RefEntity: entity}
		}
		str := func(name string) metadata.Field { return metadata.Field{Name: name, Type: metadata.FieldTypeString} }
		store := &metadata.Entity{Name: "СкладПРС", Kind: metadata.KindCatalog, Fields: []metadata.Field{str("Наименование")}}
		item := &metadata.Entity{Name: "ТоварПРС", Kind: metadata.KindCatalog, Fields: []metadata.Field{str("Наименование")}}
		place := &metadata.Entity{Name: "МестоПРС", Kind: metadata.KindCatalog, Fields: []metadata.Field{str("Наименование")}}
		order := &metadata.Entity{Name: "ОрдерПРС", Kind: metadata.KindDocument,
			Fields: []metadata.Field{str("Номер"), ref("Отправитель", store.Name)}}
		entities := []*metadata.Entity{store, item, place, order}
		reg := &metadata.Register{
			Name:       "ОстаткиПРС",
			Dimensions: []metadata.Field{ref("Склад", store.Name), ref("Номенклатура", item.Name)},
			Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
		}
		places := &metadata.InfoRegister{
			Name:       "МестаПРС",
			Dimensions: []metadata.Field{ref("Склад", store.Name), ref("Номенклатура", item.Name)},
			Resources:  []metadata.Field{ref("МестоХранения", place.Name)},
		}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{places}); err != nil {
			t.Fatal(err)
		}
		storeID, otherID, itemID, placeID, orderID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
		for _, row := range []struct {
			e  *metadata.Entity
			id uuid.UUID
			f  map[string]any
		}{
			{store, storeID, map[string]any{"Наименование": "Основной"}},
			{store, otherID, map[string]any{"Наименование": "Другой"}},
			{item, itemID, map[string]any{"Наименование": "Фильтр"}},
			{place, placeID, map[string]any{"Наименование": "А-1"}},
			{order, orderID, map[string]any{"Номер": "О-1", "Отправитель": storeID}},
		} {
			if err := db.Upsert(ctx, row.e.Name, row.id, row.f, row.e); err != nil {
				t.Fatal(err)
			}
		}
		period := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		if err := db.WriteMovements(ctx, reg.Name, order.Name, orderID, []map[string]any{
			{"ВидДвижения": "Приход", "Склад": storeID, "Номенклатура": itemID, "Количество": float64(5)},
			{"ВидДвижения": "Приход", "Склад": otherID, "Номенклатура": itemID, "Количество": float64(2)},
		}, reg, &period); err != nil {
			t.Fatal(err)
		}
		if err := db.InfoRegSet(ctx, places, map[string]any{"Склад": storeID, "Номенклатура": itemID},
			map[string]any{"МестоХранения": placeID}, nil); err != nil {
			t.Fatal(err)
		}

		for _, tc := range []struct {
			name string
			src  string
			want []string
		}{
			{
				name: "виртуальная таблица + регистр сведений в ПО",
				src: `ВЫБРАТЬ О.Склад.Наименование КАК Метка, М.МестоХранения КАК Место
					ИЗ РегистрНакопления.ОстаткиПРС.Остатки() КАК О
					ЛЕВОЕ СОЕДИНЕНИЕ РегистрСведений.МестаПРС КАК М
					ПО М.Склад = О.Склад И М.Номенклатура = О.Номенклатура
					УПОРЯДОЧИТЬ ПО Метка`,
				want: []string{"Другой:", "Основной:" + placeID.String()},
			},
			{
				name: "регистр сведений — главный источник подзапроса в соединении",
				src: `ВЫБРАТЬ О.Склад.Наименование КАК Метка, МинП.Приоритет КАК Место
					ИЗ РегистрНакопления.ОстаткиПРС.Остатки() КАК О
					ЛЕВОЕ СОЕДИНЕНИЕ (ВЫБРАТЬ МП.Склад КАК Склад, КОЛИЧЕСТВО(*) КАК Приоритет
						ИЗ РегистрСведений.МестаПРС КАК МП ГДЕ МП.Номенклатура = &Товар
						СГРУППИРОВАТЬ ПО МП.Склад) КАК МинП
					ПО МинП.Склад = О.Склад
					УПОРЯДОЧИТЬ ПО Метка`,
				want: []string{"Другой:", "Основной:1"},
			},
			{
				name: "документ + регистр сведений в ПО и ГДЕ",
				src: `ВЫБРАТЬ Д.Номер КАК Метка, М.МестоХранения КАК Место
					ИЗ Документ.ОрдерПРС КАК Д
					ЛЕВОЕ СОЕДИНЕНИЕ РегистрСведений.МестаПРС КАК М
					ПО М.Склад = Д.Отправитель
					ГДЕ М.Номенклатура = &Товар`,
				want: []string{"О-1:" + placeID.String()},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				res, err := query.Compile(tc.src, query.CompileOpts{
					Entities:  entities,
					Registers: []*metadata.Register{reg},
					InfoRegs:  []*metadata.InfoRegister{places},
					Params:    map[string]any{"Товар": itemID},
					Dialect:   db.Dialect(),
				})
				if err != nil {
					t.Fatalf("Compile: %v", err)
				}
				rows, _, err := query.Run(ctx, db, &res)
				if err != nil {
					t.Fatalf("Run: %v\nSQL: %s", err, res.SQL)
				}
				var got []string
				for _, row := range rows {
					got = append(got, stringOf(row["метка"])+":"+stringOf(row["место"]))
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("строки = %v, want %v\nSQL: %s", got, tc.want, res.SQL)
				}
			})
		}
	})
}
