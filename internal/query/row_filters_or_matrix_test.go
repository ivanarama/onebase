package query_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Скобки вокруг предиката строковой политики — на обоих диалектах (#888).
//
// Фикс #652/#625 матрично проверялся только для варианта с ВИРТУАЛЬНОЙ
// таблицей (row_filters_vt_matrix_test.go). Для обычной таблицы те же четыре
// случая — собственное ГДЕ с ИЛИ, ветвь ОБЪЕДИНИТЬ, ГДЕ с группировкой и ON
// авто-JOIN — гонялись только на SQLite с прибитым storage.SQLiteDialect{}.
//
// Приоритет операторов у диалектов одинаков, но собирается SQL по-разному:
// плейсхолдеры ($1 против ?), приведение типов, кавычки идентификаторов. Утечка
// строк мимо политики — не та вещь, о которой достаточно знать на одном
// диалекте.

func orMatrixEntities() []*metadata.Entity {
	mk := func(name string) *metadata.Entity {
		return &metadata.Entity{
			Name: name,
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Owner", Type: metadata.FieldTypeString},
			},
		}
	}
	return []*metadata.Entity{mk("ТоварМатрица"), mk("УслугаМатрица")}
}

func orMatrixFilters() map[query.SourceRef]*storage.Predicate {
	return map[query.SourceRef]*storage.Predicate{
		{Kind: "catalog", Name: "ТоварМатрица"}:  {Field: "Owner", Op: "eq", Value: "свой"},
		{Kind: "catalog", Name: "УслугаМатрица"}: {Field: "Owner", Op: "eq", Value: "свой"},
	}
}

// Политика из нескольких вариантов: одиночное условие даёт предикат без OR, и
// дефект приоритета на нём не проявляется вовсе.
func orMatrixAnyFilter() map[query.SourceRef]*storage.Predicate {
	return map[query.SourceRef]*storage.Predicate{
		{Kind: "catalog", Name: "ТоварМатрица"}: {Any: []storage.Predicate{
			{Field: "Owner", Op: "eq", Value: "свой"},
			{Field: "Owner", Op: "eq", Value: "общий"},
		}},
	}
}

func orMatrixDB(t *testing.T, db *storage.DB) {
	t.Helper()
	ctx := context.Background()
	ents := orMatrixEntities()
	if err := db.Migrate(ctx, ents); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	for _, e := range ents {
		for _, row := range []struct{ name, owner string }{
			{e.Name + "-свой", "свой"},
			{e.Name + "-чужой", "чужой"},
			{e.Name + "-общий", "общий"},
		} {
			if err := db.Upsert(ctx, e.Name, uuid.New(),
				map[string]any{"Наименование": row.name, "Owner": row.owner}, e); err != nil {
				t.Fatalf("Upsert %s: %v", row.name, err)
			}
		}
	}
}

// ИЛИ в собственном ГДЕ пользователя: правая ветвь не должна выходить из-под
// политики. Без скобок получается «(политика AND левая) OR правая».
func TestRowFiltersOrInOwnWhereMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		orMatrixDB(t, db)
		res, err := query.Compile(
			`ВЫБРАТЬ Т.Наименование ИЗ Справочник.ТоварМатрица КАК Т
			 ГДЕ Т.Наименование = &Свой ИЛИ Т.Наименование = &Чужой`,
			query.CompileOpts{
				Entities:   orMatrixEntities(),
				Dialect:    db.Dialect(),
				RowFilters: orMatrixFilters(),
				Params: map[string]any{
					"Свой":  "ТоварМатрица-свой",
					"Чужой": "ТоварМатрица-чужой",
				},
			})
		if err != nil {
			t.Fatalf("компиляция: %v", err)
		}
		assertNames(t, runNames(t, db, res), "ТоварМатрица-свой")
	})
}

// То же в ветвях ОБЪЕДИНИТЬ: ИЛИ ветви не поднимается выше её фильтра.
func TestRowFiltersUnionBranchOrMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		orMatrixDB(t, db)
		res, err := query.Compile(
			`ВЫБРАТЬ Т.Наименование ИЗ Справочник.ТоварМатрица КАК Т
			 ГДЕ Т.Наименование = &Свой ИЛИ Т.Наименование = &Чужой
			 ОБЪЕДИНИТЬ ВСЕ
			 ВЫБРАТЬ У.Наименование ИЗ Справочник.УслугаМатрица КАК У
			 ГДЕ У.Наименование = &СвояУслуга ИЛИ У.Наименование = &ЧужаяУслуга`,
			query.CompileOpts{
				Entities:   orMatrixEntities(),
				Dialect:    db.Dialect(),
				RowFilters: orMatrixFilters(),
				Params: map[string]any{
					"Свой":        "ТоварМатрица-свой",
					"Чужой":       "ТоварМатрица-чужой",
					"СвояУслуга":  "УслугаМатрица-свой",
					"ЧужаяУслуга": "УслугаМатрица-чужой",
				},
			})
		if err != nil {
			t.Fatalf("компиляция: %v", err)
		}
		assertNames(t, runNames(t, db, res), "ТоварМатрица-свой", "УслугаМатрица-свой")
	})
}

// Многовариантная политика (any) плюс ИЛИ пользователя: обе группы обязаны
// остаться каждая одним операндом. Это тот случай, где скобок нужно двое.
func TestRowFilterAnyWithUserOrMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		orMatrixDB(t, db)
		res, err := query.Compile(
			`ВЫБРАТЬ Т.Наименование ИЗ Справочник.ТоварМатрица КАК Т
			 ГДЕ Т.Наименование = &Общий ИЛИ Т.Наименование = &Чужой`,
			query.CompileOpts{
				Entities:   orMatrixEntities(),
				Dialect:    db.Dialect(),
				RowFilters: orMatrixAnyFilter(),
				Params: map[string]any{
					"Общий": "ТоварМатрица-общий",
					"Чужой": "ТоварМатрица-чужой",
				},
			})
		if err != nil {
			t.Fatalf("компиляция: %v", err)
		}
		// «общий» политика пропускает, «чужой» — нет, несмотря на ИЛИ.
		assertNames(t, runNames(t, db, res), "ТоварМатрица-общий")
	})
}

// Группировка и порядок не переносят условие политики в HAVING/ORDER мимо
// скобок: агрегат считается только по разрешённым строкам.
func TestRowFiltersOrWithGroupMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		orMatrixDB(t, db)
		res, err := query.Compile(
			`ВЫБРАТЬ Т.Owner КАК Owner, КОЛИЧЕСТВО(Т.Наименование) КАК Кол
			 ИЗ Справочник.ТоварМатрица КАК Т
			 ГДЕ Т.Наименование = &Свой ИЛИ Т.Наименование = &Чужой
			 СГРУППИРОВАТЬ ПО Т.Owner
			 УПОРЯДОЧИТЬ ПО Owner`,
			query.CompileOpts{
				Entities:   orMatrixEntities(),
				Dialect:    db.Dialect(),
				RowFilters: orMatrixFilters(),
				Params: map[string]any{
					"Свой":  "ТоварМатрица-свой",
					"Чужой": "ТоварМатрица-чужой",
				},
			})
		if err != nil {
			t.Fatalf("компиляция: %v", err)
		}
		rows, err := db.Query(context.Background(), res.SQL, res.Args...)
		if err != nil {
			t.Fatalf("выполнение: %v\n%s", err, res.SQL)
		}
		defer rows.Close()
		groups := map[string]int64{}
		for rows.Next() {
			var owner string
			var n int64
			if err := rows.Scan(&owner, &n); err != nil {
				t.Fatalf("чтение: %v", err)
			}
			groups[owner] = n
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if len(groups) != 1 || groups["свой"] != 1 {
			t.Fatalf("группы = %v, ожидалась одна «свой»=1; появление «чужой» значит, "+
				"что ИЛИ пользователя вырвалось из-под политики\nSQL: %s", groups, res.SQL)
		}
	})
}
