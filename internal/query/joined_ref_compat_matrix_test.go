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

// Совместимость отложенных JOIN (#1385) с типами разыменованных реквизитов
// текущего main (#1784): одинаковое имя ссылки у главного источника не
// определяет ни физическую колонку, ни тип поля присоединённого источника.
func TestJoinedRefMainCompatibilityMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := []*metadata.Entity{
			{Name: "Учётки", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}},
			{Name: "Ложные", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Балл", Type: metadata.FieldTypeString}, {Name: "Учётка", Type: metadata.FieldTypeString}}},
			{Name: "Базы", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Балл", Type: metadata.FieldTypeNumber}, {Name: "Учётка", Type: "reference:Учётки", RefEntity: "Учётки"}}},
			{Name: "Источники", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Код", Type: metadata.FieldTypeString}, {Name: "ГлавныйУзел", Type: "reference:Ложные", RefEntity: "Ложные"}}},
			{Name: "Планы", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Код", Type: metadata.FieldTypeString}, {Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "ГлавныйУзел", Type: "reference:Базы", RefEntity: "Базы"}}},
			{Name: "ДругиеПланы", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Код", Type: metadata.FieldTypeString}, {Name: "ГлавныйУзел", Type: "reference:Ложные", RefEntity: "Ложные"}}},
		}
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatal(err)
		}
		account, node, otherNode := uuid.New(), uuid.New(), uuid.New()
		for _, v := range []struct {
			ent    *metadata.Entity
			id     uuid.UUID
			fields map[string]any
		}{
			{ents[0], account, map[string]any{"Наименование": "Учётка"}},
			{ents[2], node, map[string]any{"Наименование": "Главная", "Балл": 10, "Учётка": account}},
			{ents[3], uuid.New(), map[string]any{"Код": "GL"}},
			{ents[4], uuid.New(), map[string]any{"Код": "GL", "Наименование": "План-1", "ГлавныйУзел": node}},
			{ents[1], otherNode, map[string]any{"Учётка": "GL", "Балл": "строка"}},
			{ents[5], uuid.New(), map[string]any{"Код": "GL", "ГлавныйУзел": otherNode}},
		} {
			if err := db.Upsert(ctx, v.ent.Name, v.id, v.fields, v.ent); err != nil {
				t.Fatal(err)
			}
		}
		const joined = ` ИЗ Справочник.Источники КАК Ист ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.Код = Ист.Код`
		for _, c := range []struct{ name, q, want, refKey, refEntity string }{
			{"ссылочный реквизит навигации", `ВЫБРАТЬ П.ГлавныйУзел.Учётка КАК Учётка` + joined, account.String(), "учётка", "Учётки"},
			{"тип числового реквизита", `ВЫБРАТЬ П.Наименование` + joined + ` ГДЕ П.ГлавныйУзел.Балл > 3`, "План-1", "", ""},
			{"тип явной ссылки", `ВЫБРАТЬ П.ГлавныйУзел.Ссылка КАК Узел` + joined, node.String(), "узел", "Базы"},
			{"постфиксный отбор", `ВЫБРАТЬ П.ГлавныйУзел.Наименование` + joined + ` ГДЕ П.ГлавныйУзел НЕ ЕСТЬ ПУСТО`, "Главная", "", ""},
			{"навигация в ПО следующего соединения", `ВЫБРАТЬ У.Наименование` + joined + ` ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Учётки КАК У ПО У.Ссылка = П.ГлавныйУзел.Учётка`, "Учётка", "", ""},
			{"голое поле сохраняет представление", `ВЫБРАТЬ ГлавныйУзел ИЗ Справочник.Планы`, "Главная", "", ""},
			{"одноимённые алиасы вложенного scope", `ВЫБРАТЬ П.Наименование` + joined + ` ГДЕ П.ГлавныйУзел.Ссылка В (ВЫБРАТЬ П.ГлавныйУзел.Ссылка ИЗ Справочник.Источники КАК Ист ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.Код = Ист.Код ГДЕ П.ГлавныйУзел.Балл > 3)`, "План-1", "", ""},
			// Сложная проекция сохраняет прежний контракт без RefColumns:
			// проверяем физические колонки разных ссылок у одноимённых алиасов.
			{"одинаковый алиас с разными типами ссылки в SELECT-scope", `ВЫБРАТЬ П.ГлавныйУзел.Учётка КАК Учётка` + joined + ` ГДЕ П.Код В (ВЫБРАТЬ П.ГлавныйУзел.Учётка ИЗ Справочник.Источники КАК Ист ЛЕВОЕ СОЕДИНЕНИЕ Справочник.ДругиеПланы КАК П ПО П.Код = Ист.Код)`, account.String(), "", ""},
			{"атрибут внутри производной таблицы", `ВЫБРАТЬ Под.Учётка ИЗ (ВЫБРАТЬ П.ГлавныйУзел.Учётка КАК Учётка` + joined + `) КАК Под`, account.String(), "", ""},
		} {
			t.Run(c.name, func(t *testing.T) {
				r, err := query.Compile(c.q, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
				if err != nil {
					t.Fatal(err)
				}
				if c.refKey != "" && r.RefColumns[c.refKey] != c.refEntity {
					t.Errorf("RefColumns=%v, ожидалась %s → %s", r.RefColumns, c.refKey, c.refEntity)
				}
				rows, err := db.Query(ctx, r.SQL, r.Args...)
				if err != nil {
					t.Fatalf("%v\nSQL: %s", err, r.SQL)
				}
				defer rows.Close()
				var got []string
				for rows.Next() {
					var v any
					if err := rows.Scan(&v); err != nil {
						t.Fatal(err)
					}
					if id, ok := v.([16]byte); ok {
						v = uuid.UUID(id).String()
					}
					got = append(got, fmt.Sprint(v))
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, []string{c.want}) {
					t.Fatalf("получено %v, ожидалось %q\nSQL: %s", got, c.want, r.SQL)
				}
			})
		}
	})
}
