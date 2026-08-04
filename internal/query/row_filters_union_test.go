package query_test

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Строковые политики и ОБЪЕДИНИТЬ (план 79).
//
// Проверяем поведение, а не форму SQL: фильтр ветви попадает то в её WHERE, то
// в скоуп-подзапрос источника — и то и другое правильно. Важно единственное:
// из каждой ветви возвращаются только свои строки. Поэтому запросы здесь
// по-настоящему исполняются на SQLite.

func unionEntities() []*metadata.Entity {
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
	return []*metadata.Entity{mk("Товар"), mk("Услуга")}
}

func unionFilters() map[query.SourceRef]*storage.Predicate {
	return map[query.SourceRef]*storage.Predicate{
		{Kind: "catalog", Name: "Товар"}:  {Field: "Owner", Op: "eq", Value: "свой"},
		{Kind: "catalog", Name: "Услуга"}: {Field: "Owner", Op: "eq", Value: "свой"},
	}
}

// unionDB создаёт базу, где у каждого справочника есть строка своего владельца
// и строка чужого.
func unionDB(t *testing.T) *storage.DB {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "union.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	ents := unionEntities()
	if err := db.Migrate(ctx, ents); err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		for _, row := range []struct{ name, owner string }{
			{e.Name + "-свой", "свой"},
			{e.Name + "-чужой", "чужой"},
		} {
			if err := db.Upsert(ctx, e.Name, uuid.New(),
				map[string]any{"Наименование": row.name, "Owner": row.owner}, e); err != nil {
				t.Fatal(err)
			}
		}
	}
	return db
}

// runNames исполняет скомпилированный запрос и возвращает первую колонку.
func runNames(t *testing.T, db *storage.DB, res query.Result) []string {
	t.Helper()
	rows, err := db.Query(context.Background(), res.SQL, res.Args...)
	if err != nil {
		t.Fatalf("выполнение запроса: %v\n%s", err, res.SQL)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("чтение строки: %v", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func assertNames(t *testing.T, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("строки %v, ожидались %v", got, want)
	}
}

// Обе ветви объединения фильтруются по своей политике. Раньше такой запрос
// отклонялся целиком: отложенный фильтр главной таблицы уходил во внешний
// WHERE, который относится только к одной ветви.
func TestCompile_RowFiltersUnionFiltersBothBranches(t *testing.T) {
	db := unionDB(t)
	res, err := query.Compile(
		`ВЫБРАТЬ Т.Наименование ИЗ Справочник.Товар КАК Т
		 ОБЪЕДИНИТЬ ВСЕ
		 ВЫБРАТЬ У.Наименование ИЗ Справочник.Услуга КАК У`,
		query.CompileOpts{
			Entities:   unionEntities(),
			Dialect:    storage.SQLiteDialect{},
			RowFilters: unionFilters(),
		})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	assertNames(t, runNames(t, db, res), "Товар-свой", "Услуга-свой")
}

// Собственное условие ветви не теряется: политика добавляется к нему.
func TestCompile_RowFiltersUnionKeepsOwnWhere(t *testing.T) {
	db := unionDB(t)
	res, err := query.Compile(
		`ВЫБРАТЬ Т.Наименование ИЗ Справочник.Товар КАК Т ГДЕ Т.Наименование <> &Нет
		 ОБЪЕДИНИТЬ ВСЕ
		 ВЫБРАТЬ У.Наименование ИЗ Справочник.Услуга КАК У ГДЕ У.Наименование <> &Нет`,
		query.CompileOpts{
			Entities:   unionEntities(),
			Params:     map[string]any{"Нет": "Услуга-свой"},
			Dialect:    storage.SQLiteDialect{},
			RowFilters: unionFilters(),
		})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// Политика оставляет только «свои», собственное условие убирает Услуга-свой.
	assertNames(t, runNames(t, db, res), "Товар-свой")
}

// Упорядочивание относится ко всему объединению и не ломает фильтрацию ветвей.
func TestCompile_RowFiltersUnionWithOrderBy(t *testing.T) {
	db := unionDB(t)
	res, err := query.Compile(
		`ВЫБРАТЬ Т.Наименование КАК Имя ИЗ Справочник.Товар КАК Т
		 ОБЪЕДИНИТЬ ВСЕ
		 ВЫБРАТЬ У.Наименование КАК Имя ИЗ Справочник.Услуга КАК У
		 УПОРЯДОЧИТЬ ПО Имя`,
		query.CompileOpts{
			Entities:   unionEntities(),
			Dialect:    storage.SQLiteDialect{},
			RowFilters: unionFilters(),
		})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	assertNames(t, runNames(t, db, res), "Товар-свой", "Услуга-свой")
}

// Политика только у одной из объединяемых сущностей: вторая ветвь возвращает
// всё, первая — только своё.
func TestCompile_RowFiltersUnionSingleFilteredSource(t *testing.T) {
	db := unionDB(t)
	res, err := query.Compile(
		`ВЫБРАТЬ Т.Наименование ИЗ Справочник.Товар КАК Т
		 ОБЪЕДИНИТЬ ВСЕ
		 ВЫБРАТЬ У.Наименование ИЗ Справочник.Услуга КАК У`,
		query.CompileOpts{
			Entities: unionEntities(),
			Dialect:  storage.SQLiteDialect{},
			RowFilters: map[query.SourceRef]*storage.Predicate{
				{Kind: "catalog", Name: "Товар"}: {Field: "Owner", Op: "eq", Value: "свой"},
			},
		})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	assertNames(t, runNames(t, db, res), "Товар-свой", "Услуга-свой", "Услуга-чужой")
}

// Три ветви: проверяем, что накопление фильтров сбрасывается на каждой границе,
// а не только на первой.
func TestCompile_RowFiltersUnionThreeBranches(t *testing.T) {
	db := unionDB(t)
	res, err := query.Compile(
		`ВЫБРАТЬ Т.Наименование ИЗ Справочник.Товар КАК Т
		 ОБЪЕДИНИТЬ ВСЕ
		 ВЫБРАТЬ У.Наименование ИЗ Справочник.Услуга КАК У
		 ОБЪЕДИНИТЬ ВСЕ
		 ВЫБРАТЬ Т2.Наименование ИЗ Справочник.Товар КАК Т2`,
		query.CompileOpts{
			Entities:   unionEntities(),
			Dialect:    storage.SQLiteDialect{},
			RowFilters: unionFilters(),
		})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	assertNames(t, runNames(t, db, res), "Товар-свой", "Товар-свой", "Услуга-свой")
}

// Ветвь с соединением: источник фильтруется скоуп-подзапросом, и сброс
// накопления на границе ветвей этому не мешает.
func TestCompile_RowFiltersUnionBranchWithJoin(t *testing.T) {
	db := unionDB(t)
	res, err := query.Compile(
		`ВЫБРАТЬ Т.Наименование ИЗ Справочник.Товар КАК Т
		 ОБЪЕДИНИТЬ ВСЕ
		 ВЫБРАТЬ У.Наименование ИЗ Справочник.Услуга КАК У
		   ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Товар КАК Т2 ПО Т2.Owner = У.Owner`,
		query.CompileOpts{
			Entities:   unionEntities(),
			Dialect:    storage.SQLiteDialect{},
			RowFilters: unionFilters(),
		})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// Услуга-свой соединяется с единственным своим товаром — одна строка.
	assertNames(t, runNames(t, db, res), "Товар-свой", "Услуга-свой")
}
