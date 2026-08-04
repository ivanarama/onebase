package query_test

import (
	"testing"

	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Приоритет операторов и внедрённый фильтр политики (план 79).
//
// Фильтр внедряется в начало собственного ГДЕ: «WHERE фильтр AND <условие>».
// Без скобок вокруг условия верхнеуровневое ИЛИ связывается сильнее фильтра —
// «(фильтр AND a) OR b» — и строки правой части возвращаются мимо политики.
// Проверяем исполнением: чужая строка не должна попадать в выдачу.

func TestCompile_RowFiltersOrInOwnWhereStaysFiltered(t *testing.T) {
	db := unionDB(t)
	res, err := query.Compile(
		`ВЫБРАТЬ Т.Наименование ИЗ Справочник.Товар КАК Т
		 ГДЕ Т.Наименование = &Свой ИЛИ Т.Наименование = &Чужой`,
		query.CompileOpts{
			Entities:   unionEntities(),
			Dialect:    storage.SQLiteDialect{},
			RowFilters: unionFilters(),
			Params:     map[string]any{"Свой": "Товар-свой", "Чужой": "Товар-чужой"},
		})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	assertNames(t, runNames(t, db, res), "Товар-свой")
}

// То же в ветвях объединения: ИЛИ в каждой ветви не поднимается выше фильтра.
func TestCompile_RowFiltersUnionOrInBranchWhereStaysFiltered(t *testing.T) {
	db := unionDB(t)
	res, err := query.Compile(
		`ВЫБРАТЬ Т.Наименование ИЗ Справочник.Товар КАК Т
		 ГДЕ Т.Наименование = &СвойТ ИЛИ Т.Наименование = &ЧужойТ
		 ОБЪЕДИНИТЬ ВСЕ
		 ВЫБРАТЬ У.Наименование ИЗ Справочник.Услуга КАК У
		 ГДЕ У.Наименование = &СвойУ ИЛИ У.Наименование = &ЧужойУ`,
		query.CompileOpts{
			Entities:   unionEntities(),
			Dialect:    storage.SQLiteDialect{},
			RowFilters: unionFilters(),
			Params: map[string]any{
				"СвойТ": "Товар-свой", "ЧужойТ": "Товар-чужой",
				"СвойУ": "Услуга-свой", "ЧужойУ": "Услуга-чужой",
			},
		})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	assertNames(t, runNames(t, db, res), "Товар-свой", "Услуга-свой")
}

// Скобка вокруг условия закрывается до СГРУППИРОВАТЬ/УПОРЯДОЧИТЬ — иначе SQL
// не скомпилируется, а фильтр уехал бы в группировку.
func TestCompile_RowFiltersOrWithGroupAndOrder(t *testing.T) {
	db := unionDB(t)
	res, err := query.Compile(
		`ВЫБРАТЬ Т.Наименование ИЗ Справочник.Товар КАК Т
		 ГДЕ Т.Наименование = &Свой ИЛИ Т.Наименование = &Чужой
		 СГРУППИРОВАТЬ ПО Т.Наименование
		 УПОРЯДОЧИТЬ ПО Т.Наименование`,
		query.CompileOpts{
			Entities:   unionEntities(),
			Dialect:    storage.SQLiteDialect{},
			RowFilters: unionFilters(),
			Params:     map[string]any{"Свой": "Товар-свой", "Чужой": "Товар-чужой"},
		})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	assertNames(t, runNames(t, db, res), "Товар-свой")
}
