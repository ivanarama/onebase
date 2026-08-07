package query_test

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/metadata"
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

// #625: политика any: из нескольких условий даёт предикат «a OR b». В ВТ регистра
// он ANDится с границей периода; без скобок «period<=? AND a OR b» разбирается как
// «(period<=? AND a) OR b» — для ветки b граница периода теряется, сальдо неверно.
// #574 закрыл только верхний ГДЕ; здесь — ВТ регистра.
func TestCompile_RowFiltersAnyKeepsPeriodBoundInRegisterVT(t *testing.T) {
	reg := &metadata.Register{
		Name: "ТоварноеДвижение",
		Dimensions: []metadata.Field{
			{Name: "Номенклатура", Type: metadata.FieldTypeString},
			{Name: "Склад", Type: metadata.FieldTypeString},
		},
		Resources: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	res, err := query.Compile(
		`ВЫБРАТЬ Номенклатура, КоличествоОстаток ИЗ РегистрНакопления.ТоварноеДвижение.Остатки(&Дата)`,
		query.CompileOpts{
			Registers: []*metadata.Register{reg},
			Dialect:   storage.SQLiteDialect{},
			Params:    map[string]any{"Дата": "2026-01-31"},
			RowFilters: map[query.SourceRef]*storage.Predicate{
				{Kind: "register", Name: "ТоварноеДвижение"}: {Any: []storage.Predicate{
					{Field: "Склад", Op: "eq", Value: "A"},
					{Field: "Склад", Op: "eq", Value: "B"},
				}},
			},
		})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// Граница периода должна ANDиться с ПОЛНЫМ предикатом политики, обёрнутым в
	// скобки, а не с одной его ветвью.
	if !strings.Contains(res.SQL, "period <= ? AND ((склад = ?) OR (склад = ?))") {
		t.Fatalf("предикат any: не обёрнут в скобки — граница периода теряется:\n%s", res.SQL)
	}
}

// #625, второй сайт: авто-JOIN разыменования ссылочного поля кладёт предикат
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
