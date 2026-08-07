package query_test

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestCompile_RowFiltersSimpleSourceAlias(t *testing.T) {
	cat := &metadata.Entity{
		Name: "Товар",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Owner", Type: metadata.FieldTypeString},
		},
	}
	res, err := query.Compile(`ВЫБРАТЬ Т.Наименование ИЗ Справочник.Товар КАК Т ГДЕ Т.Наименование <> &Пусто`, query.CompileOpts{
		Entities: []*metadata.Entity{cat},
		Params:   map[string]any{"Пусто": ""},
		Dialect:  storage.SQLiteDialect{},
		RowFilters: map[query.SourceRef]*storage.Predicate{
			{Kind: "catalog", Name: "Товар"}: {Field: "Owner", Op: "eq", Value: "u"},
		},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.Contains(res.SQL, "WHERE (т.owner = ?) AND") {
		t.Fatalf("row filter must be injected after WHERE with source alias, got:\n%s", res.SQL)
	}
	if len(res.Args) != 2 || res.Args[0] != "u" || res.Args[1] != "" {
		t.Fatalf("args = %#v, want row filter arg before query WHERE arg", res.Args)
	}
}

func TestCompile_RowFiltersReferenceAttribute(t *testing.T) {
	client := &metadata.Entity{
		Name:   "Клиент",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Owner", Type: metadata.FieldTypeString}},
	}
	order := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Клиент", Type: metadata.FieldTypeString, RefEntity: client.Name},
		},
	}
	res, err := query.Compile(`ВЫБРАТЬ З.Ссылка ИЗ Документ.Заказ КАК З`, query.CompileOpts{
		Entities: []*metadata.Entity{order, client},
		Dialect:  storage.SQLiteDialect{},
		RowFilters: map[query.SourceRef]*storage.Predicate{
			{Kind: "document", Name: order.Name}: {
				Field:     "Клиент",
				RefEntity: client,
				RefPredicate: &storage.Predicate{
					Field: "Owner",
					Op:    "eq",
					Value: "u",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.Contains(res.SQL, "EXISTS (SELECT 1 FROM клиент rls_ref WHERE rls_ref.id = з.клиент_id AND (rls_ref.owner = ?))") {
		t.Fatalf("reference row filter must compile to EXISTS, got:\n%s", res.SQL)
	}
	if len(res.Args) != 1 || res.Args[0] != "u" {
		t.Fatalf("args = %#v, want owner value", res.Args)
	}
}

func TestCompile_RowFiltersInsertedBeforeOrder(t *testing.T) {
	cat := &metadata.Entity{
		Name:   "Товар",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Owner", Type: metadata.FieldTypeString}},
	}
	res, err := query.Compile(`ВЫБРАТЬ Ссылка ИЗ Справочник.Товар УПОРЯДОЧИТЬ ПО Ссылка`, query.CompileOpts{
		Entities: []*metadata.Entity{cat},
		Dialect:  storage.SQLiteDialect{},
		RowFilters: map[query.SourceRef]*storage.Predicate{
			{Kind: "catalog", Name: "Товар"}: {Field: "Owner", Op: "eq", Value: "u"},
		},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.Contains(res.SQL, "WHERE (товар.owner = ?) ORDER BY") {
		t.Fatalf("row filter must be inserted before ORDER BY, got:\n%s", res.SQL)
	}
}

func TestCompile_RowFiltersVirtualRegister(t *testing.T) {
	reg := &metadata.Register{
		Name: "ТоварноеДвижение",
		Dimensions: []metadata.Field{
			{Name: "Номенклатура", Type: metadata.FieldTypeString},
			{Name: "Owner", Type: metadata.FieldTypeString},
		},
		Resources: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	res, err := query.Compile(`ВЫБРАТЬ Номенклатура, КоличествоОстаток ИЗ РегистрНакопления.ТоварноеДвижение.Остатки()`, query.CompileOpts{
		Registers: []*metadata.Register{reg},
		Dialect:   storage.SQLiteDialect{},
		RowFilters: map[query.SourceRef]*storage.Predicate{
			{Kind: "register", Name: "ТоварноеДвижение"}: {Field: "Owner", Op: "eq", Value: "u"},
		},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// Проверяем РАЗМЕЩЕНИЕ фильтра (внутри подзапроса виртуальной таблицы, до
	// GROUP BY), а не пунктуацию: с issue #625 предикат политики обрамляется
	// скобками в rowFilterCondition, и сравнение с точным текстом ломалось на
	// «WHERE (owner = ?)» при полностью верном поведении.
	noParens := strings.NewReplacer("(", "", ")", "").Replace(res.SQL)
	if !strings.Contains(noParens, "FROM рег_товарноедвижение WHERE owner = ? GROUP BY") {
		t.Fatalf("row filter must be inside register virtual table, got:\n%s", res.SQL)
	}
}

func TestCompile_RowFiltersJoinedSourceScopedBeforeOn(t *testing.T) {
	doc := &metadata.Entity{Name: "Заказ", Kind: metadata.KindDocument}
	cat := &metadata.Entity{
		Name: "Клиент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Owner", Type: metadata.FieldTypeString},
		},
	}
	res, err := query.Compile(`ВЫБРАТЬ з.Ссылка, к.Наименование ИЗ Документ.Заказ КАК з ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Клиент КАК к ПО к.Ссылка = з.Ссылка`, query.CompileOpts{
		Entities: []*metadata.Entity{doc, cat},
		Dialect:  storage.SQLiteDialect{},
		RowFilters: map[query.SourceRef]*storage.Predicate{
			{Kind: "catalog", Name: "Клиент"}: {Field: "Owner", Op: "eq", Value: "u"},
		},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.Contains(res.SQL, "LEFT JOIN (SELECT * FROM клиент WHERE owner = ?) AS к ON") {
		t.Fatalf("joined row filter must be scoped inside joined source, got:\n%s", res.SQL)
	}
	if strings.Contains(res.SQL, "WHERE (к.owner = ?)") {
		t.Fatalf("joined row filter must not turn LEFT JOIN into an outer WHERE filter:\n%s", res.SQL)
	}
	if len(res.Args) != 1 || res.Args[0] != "u" {
		t.Fatalf("args = %#v, want one joined row filter arg", res.Args)
	}
}

func TestCompile_RowFiltersSkipNestedWhereBeforeOuterScope(t *testing.T) {
	product := &metadata.Entity{
		Name: "Товар",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Owner", Type: metadata.FieldTypeString},
		},
	}
	client := &metadata.Entity{
		Name: "Клиент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	res, err := query.Compile(`ВЫБРАТЬ т.Ссылка
ИЗ Справочник.Товар КАК т
ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Клиент КАК к
ПО к.Ссылка В (ВЫБРАТЬ Ссылка ИЗ Справочник.Клиент ГДЕ Наименование <> &Пусто)`, query.CompileOpts{
		Entities: []*metadata.Entity{product, client},
		Params:   map[string]any{"Пусто": ""},
		Dialect:  storage.SQLiteDialect{},
		RowFilters: map[query.SourceRef]*storage.Predicate{
			{Kind: "catalog", Name: "Товар"}: {Field: "Owner", Op: "eq", Value: "u"},
		},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if strings.Contains(res.SQL, "WHERE (т.owner = ?) AND наименование") {
		t.Fatalf("main row filter must not be injected into nested WHERE:\n%s", res.SQL)
	}
	if !strings.Contains(res.SQL, ") WHERE (т.owner = ?)") {
		t.Fatalf("main row filter must be emitted at outer query level, got:\n%s", res.SQL)
	}
	if len(res.Args) != 2 || res.Args[0] != "" || res.Args[1] != "u" {
		t.Fatalf("args = %#v, want nested param first, then outer row filter", res.Args)
	}
}

func TestCompile_RowFiltersSubqueryInFromScoped(t *testing.T) {
	cat := &metadata.Entity{
		Name:   "Товар",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Owner", Type: metadata.FieldTypeString}},
	}
	res, err := query.Compile(`ВЫБРАТЬ * ИЗ (ВЫБРАТЬ Ссылка ИЗ Справочник.Товар) КАК П`, query.CompileOpts{
		Entities: []*metadata.Entity{cat},
		Dialect:  storage.SQLiteDialect{},
		RowFilters: map[query.SourceRef]*storage.Predicate{
			{Kind: "catalog", Name: "Товар"}: {Field: "Owner", Op: "eq", Value: "u"},
		},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.Contains(res.SQL, "SELECT id FROM (SELECT * FROM товар WHERE owner = ?) AS товар") {
		t.Fatalf("restricted source inside FROM subquery must be scoped, got:\n%s", res.SQL)
	}
	if len(res.Args) != 1 || res.Args[0] != "u" {
		t.Fatalf("args = %#v, want row filter arg", res.Args)
	}
}

func TestCompile_RowFiltersSubqueryInFromScopedAliasAndWhere(t *testing.T) {
	cat := &metadata.Entity{
		Name: "Товар",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Owner", Type: metadata.FieldTypeString},
		},
	}
	res, err := query.Compile(`ВЫБРАТЬ П.Наименование
ИЗ (ВЫБРАТЬ Т.Наименование ИЗ Справочник.Товар КАК Т ГДЕ Т.Наименование <> &Пусто) КАК П`, query.CompileOpts{
		Entities: []*metadata.Entity{cat},
		Params:   map[string]any{"Пусто": ""},
		Dialect:  storage.SQLiteDialect{},
		RowFilters: map[query.SourceRef]*storage.Predicate{
			{Kind: "catalog", Name: "Товар"}: {Field: "Owner", Op: "eq", Value: "u"},
		},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.Contains(res.SQL, "FROM (SELECT * FROM товар WHERE owner = ?) AS т WHERE т.наименование <> ?") {
		t.Fatalf("restricted aliased source inside FROM subquery must be scoped before local WHERE, got:\n%s", res.SQL)
	}
	if len(res.Args) != 2 || res.Args[0] != "u" || res.Args[1] != "" {
		t.Fatalf("args = %#v, want row filter arg before local WHERE arg", res.Args)
	}
}

// TestCompile_SubqueryInFromOpenDeployment: без активных строковых политик
// подзапрос в ИЗ по-прежнему компилируется (отказ касается только ограниченных
// источников — pred!=nil).
func TestCompile_SubqueryInFromOpenDeployment(t *testing.T) {
	cat := &metadata.Entity{
		Name:   "Товар",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	res, err := query.Compile(`ВЫБРАТЬ * ИЗ (ВЫБРАТЬ Ссылка ИЗ Справочник.Товар) КАК П`, query.CompileOpts{
		Entities: []*metadata.Entity{cat},
		Dialect:  storage.SQLiteDialect{},
	})
	if err != nil {
		t.Fatalf("open deployment must still compile FROM subqueries: %v", err)
	}
	if !strings.Contains(res.SQL, "SELECT id FROM товар") {
		t.Fatalf("FROM subquery must survive without row filters, got:\n%s", res.SQL)
	}
}
