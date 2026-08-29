package query_test

// Result.RefColumns — контракт для потребителя результата: какие колонки несут
// идентификатор ссылки и на какую сущность (#1150).
//
// Границы важнее самого списка. Голый ссылочный реквизит в списке выборки
// разворачивается в наименование связанной сущности, а не в её идентификатор, —
// принять его за ссылку значило бы отдать DSL ссылку с наименованием вместо
// UUID. И наоборот: `Номер КАК Ссылка` получает SQL-алиас `id`, но ссылкой не
// становится.

import (
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
)

func refColumnEntities() []*metadata.Entity {
	return []*metadata.Entity{
		{
			Name: "Приход",
			Kind: metadata.KindDocument,
			Fields: []metadata.Field{
				{Name: "Номер", Type: metadata.FieldTypeString},
				{Name: "Проект", Type: "reference:Проект", RefEntity: "Проект"},
			},
		},
		{
			Name:   "Проект",
			Kind:   metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		},
	}
}

func refColumnRegisters() []*metadata.Register {
	return []*metadata.Register{{
		Name:       "Остатки",
		Dimensions: []metadata.Field{{Name: "Проект", Type: "reference:Проект", RefEntity: "Проект"}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}}
}

func TestRefColumns(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "голая Ссылка уходит в колонку id",
			src:  `ВЫБРАТЬ Ссылка, Номер ИЗ Документ.Приход`,
			want: map[string]string{"id": "Приход"},
		},
		{
			name: "алиас вывода становится именем колонки",
			src:  `ВЫБРАТЬ Ссылка КАК Док ИЗ Документ.Приход`,
			want: map[string]string{"док": "Приход"},
		},
		{
			name: "ссылка через алиас источника",
			src:  `ВЫБРАТЬ р.Ссылка КАК Док ИЗ Документ.Приход КАК р`,
			want: map[string]string{"док": "Приход"},
		},
		{
			name: "ссылка через имя источника",
			src:  `ВЫБРАТЬ Приход.Ссылка КАК Док ИЗ Документ.Приход`,
			want: map[string]string{"док": "Приход"},
		},
		{
			// «КАК Ссылка» не создаёт колонку «ссылка»: имя зарезервировано, и
			// SQL-алиасом остаётся id. Ключ карты обязан совпасть с колонкой
			// результата, иначе обёртка молча не сработает.
			name: "псевдоним зарезервированным именем остаётся колонкой id",
			src:  `ВЫБРАТЬ Ссылка КАК Ссылка ИЗ Документ.Приход`,
			want: map[string]string{"id": "Приход"},
		},
		{
			name: "разыменование ссылочного реквизита даёт связанную сущность",
			src:  `ВЫБРАТЬ Проект.Ссылка КАК ПроектСсылка ИЗ Документ.Приход`,
			want: map[string]string{"проектссылка": "Проект"},
		},
		{
			name: "измерение регистра разыменовывается так же",
			src:  `ВЫБРАТЬ Проект.Ссылка КАК ПроектСсылка ИЗ РегистрНакопления.Остатки`,
			want: map[string]string{"проектссылка": "Проект"},
		},
		{
			name: "голый ссылочный реквизит — представление, а не ссылка",
			src:  `ВЫБРАТЬ Проект ИЗ Документ.Приход`,
			want: nil,
		},
		{
			name: "чужое поле под именем Ссылка ссылкой не становится",
			src:  `ВЫБРАТЬ Номер КАК Ссылка ИЗ Документ.Приход`,
			want: nil,
		},
		{
			name: "агрегат над ссылкой — число",
			src:  `ВЫБРАТЬ КОЛИЧЕСТВО(Ссылка) КАК К ИЗ Документ.Приход`,
			want: nil,
		},
		{
			name: "у регистра собственной ссылки нет",
			src:  `ВЫБРАТЬ Количество ИЗ РегистрНакопления.Остатки`,
			want: nil,
		},
		{
			name: "ОБЪЕДИНИТЬ — соответствие колонок неоднозначно",
			src:  `ВЫБРАТЬ Ссылка ИЗ Документ.Приход ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Ссылка ИЗ Документ.Приход`,
			want: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := query.Compile(c.src, query.CompileOpts{
				Entities:  refColumnEntities(),
				Registers: refColumnRegisters(),
			})
			if err != nil {
				t.Fatalf("компиляция: %v\nSQL-источник: %s", err, c.src)
			}
			if len(r.RefColumns) != len(c.want) {
				t.Fatalf("RefColumns = %v, ожидалось %v\nSQL: %s", r.RefColumns, c.want, r.SQL)
			}
			for col, ent := range c.want {
				if got := r.RefColumns[col]; got != ent {
					t.Errorf("RefColumns[%q] = %q, ожидалось %q\nSQL: %s", col, got, ent, r.SQL)
				}
			}
		})
	}
}
