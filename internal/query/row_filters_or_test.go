package query_test

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
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

// Виртуальная таблица регистра — второй сайт того же дефекта — покрыта
// поведенчески и на обоих диалектах в row_filters_vt_matrix_test.go
// (TestRowFilterAnyKeepsMomentBoundInVirtualTable): там сверяется само сальдо,
// а не текст SQL. Дублировать её здесь сверкой с точной пунктуацией незачем —
// ровно такую сверку выше по файлу пришлось снимать, она ломалась на верном
// поведении.

// #625: авто-JOIN разыменования ссылочного поля кладёт предикат
// политики в ON. Без скобок «ON id=fk AND a OR b» связывает JOIN только с a, а
// строки b примешивают чужую ссылку (и размножают строку).
func TestCompile_RowFiltersAnyWrappedInAutoJoinON(t *testing.T) {
	cat := &metadata.Entity{
		Name: "Категория", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Owner", Type: metadata.FieldTypeString},
		},
	}
	tov := &metadata.Entity{
		Name: "Товар", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Категория", Type: metadata.FieldTypeString, RefEntity: "Категория"}},
	}
	res, err := query.Compile(`ВЫБРАТЬ Т.Категория.Наименование ИЗ Справочник.Товар КАК Т`, query.CompileOpts{
		Entities: []*metadata.Entity{cat, tov},
		Dialect:  storage.SQLiteDialect{},
		RowFilters: map[query.SourceRef]*storage.Predicate{
			{Kind: "catalog", Name: "Категория"}: {Any: []storage.Predicate{
				{Field: "Owner", Op: "eq", Value: "A"},
				{Field: "Owner", Op: "eq", Value: "B"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.Contains(res.SQL, "AND ((ref_категория.owner = ?) OR (ref_категория.owner = ?))") {
		t.Fatalf("предикат any: в ON авто-JOIN не обёрнут в скобки:\n%s", res.SQL)
	}
}
