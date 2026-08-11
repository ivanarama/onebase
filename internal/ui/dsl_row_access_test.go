package ui

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

func dslRLSTestUser(login string, entity string, ops ...string) *auth.User {
	return &auth.User{Login: login, Roles: []*auth.Role{{
		Permissions: auth.Permission{
			Catalogs: map[string][]string{entity: ops},
			RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
				entity: {"read": {Field: "Owner", Op: "eq", Value: auth.RowValue{User: "login"}}},
			}},
		},
	}}}
}

func dslRLSTestServer(t *testing.T) (*Server, context.Context, *metadata.Entity) {
	t.Helper()
	cat := &metadata.Entity{
		Name: "Товар", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Owner", Type: metadata.FieldTypeString},
		},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})
	if err := s.store.Upsert(ctx, cat.Name, uuid.New(), map[string]any{"Наименование": "Allowed", "Owner": "u"}, cat); err != nil {
		t.Fatalf("upsert allowed: %v", err)
	}
	if err := s.store.Upsert(ctx, cat.Name, uuid.New(), map[string]any{"Наименование": "Hidden", "Owner": "other"}, cat); err != nil {
		t.Fatalf("upsert hidden: %v", err)
	}
	return s, ctx, cat
}

func runDSLRowAccessFunc(t *testing.T, s *Server, ctx context.Context, src string) any {
	t.Helper()
	prog := mustParse(t, src)
	var result any
	vars, _ := s.buildDSLVarsTx(ctx, runtime.NewMovementsCollector("test", uuid.Nil))
	if err := s.interp.RunWithResult(prog.Procedures[0], nil, &result, vars); err != nil {
		t.Fatalf("run DSL: %v", err)
	}
	return result
}

func TestDSLQuery_RowAccessFiltersRows(t *testing.T) {
	s, _, _ := dslRLSTestServer(t)
	ctx := auth.ContextWithUser(context.Background(), dslRLSTestUser("u", "Товар", "read"))

	result := runDSLRowAccessFunc(t, s, ctx, `Функция Проверка() Экспорт
  З = Новый Запрос;
  З.Текст = "ВЫБРАТЬ Наименование ИЗ Справочник.Товар";
  Р = З.Выполнить();
  Возврат Р.Количество();
КонецФункции`)
	if result != float64(1) {
		t.Fatalf("DSL query row count = %v, want 1", result)
	}
}

func TestDSLCatalogFind_RowAccessHidesRows(t *testing.T) {
	s, _, _ := dslRLSTestServer(t)
	ctx := auth.ContextWithUser(context.Background(), dslRLSTestUser("u", "Товар", "read"))
	vars, _ := s.buildDSLVarsTx(ctx, runtime.NewMovementsCollector("test", uuid.Nil))
	catalogs := vars["Справочники"].(*interpreter.CatalogsRoot)
	proxy := catalogs.Get("Товар").(*interpreter.CatalogProxy)

	if got := proxy.CallMethod("найтипонаименованию", []any{"Hidden"}); got != nil {
		t.Fatalf("hidden row must not be found, got %T %+v", got, got)
	}
	if got := proxy.CallMethod("найтипонаименованию", []any{"Allowed"}); got == nil {
		t.Fatal("allowed row must be found")
	}
}

func TestDSLCatalogMatch_FiltersEveryDuplicateBeforeCounting(t *testing.T) {
	s, ctx, cat := dslRLSTestServer(t)
	hiddenID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	allowedID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	if err := s.store.Upsert(ctx, cat.Name, hiddenID, map[string]any{"Наименование": "Same", "Owner": "other"}, cat); err != nil {
		t.Fatalf("upsert hidden duplicate: %v", err)
	}
	if err := s.store.Upsert(ctx, cat.Name, allowedID, map[string]any{"Наименование": "Same", "Owner": "u"}, cat); err != nil {
		t.Fatalf("upsert allowed duplicate: %v", err)
	}
	userCtx := auth.ContextWithUser(context.Background(), dslRLSTestUser("u", cat.Name, "read"))
	vars, _ := s.buildDSLVarsTx(userCtx, runtime.NewMovementsCollector("test", uuid.Nil))
	proxy := vars["Справочники"].(*interpreter.CatalogsRoot).Get(cat.Name).(*interpreter.CatalogProxy)

	found, ok := proxy.CallMethod("найтипонаименованию", []any{"Same"}).(*interpreter.Ref)
	if !ok || found.UUID != allowedID.String() {
		t.Fatalf("find returned %#v, want visible duplicate %s", found, allowedID)
	}
	match := proxy.CallMethod("проверитьсовпадениепореквизиту", []any{"Наименование", "Same"}).(*interpreter.Struct)
	if got := match.Get("Количество"); got != float64(1) {
		t.Fatalf("visible match count = %v, want 1", got)
	}
	ref, ok := match.Get("Ссылка").(*interpreter.Ref)
	if !ok || ref.UUID != allowedID.String() {
		t.Fatalf("match reference = %#v, want %s", ref, allowedID)
	}
}

func TestTrustedOnWriteDSL_BypassesRowAccess(t *testing.T) {
	s, ctx, cat := dslRLSTestServer(t)
	doc := &metadata.Entity{
		Name: "Событие", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	s.reg.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{cat, doc},
		Programs: map[string]*ast.Program{
			doc.Name: mustParse(t, `Процедура OnWrite() Экспорт
  З = Новый Запрос;
  З.Текст = "ВЫБРАТЬ Наименование ИЗ Справочник.Товар";
  Р = З.Выполнить();
  this.Количество = Р.Количество();
КонецПроцедуры`),
		},
	})
	userCtx := auth.ContextWithUser(ctx, dslRLSTestUser("u", "Товар", "read"))
	obj := runtime.NewObject(doc.Name, doc.Kind)
	mc := runtime.NewMovementsCollector(doc.Name, obj.ID)

	if errMsg, _ := s.runOnWriteCtx(userCtx, obj, mc); errMsg != "" {
		t.Fatalf("OnWrite error: %s", errMsg)
	}
	if got := obj.Get("Количество"); got != float64(2) {
		t.Fatalf("trusted OnWrite query count = %v, want 2", got)
	}
}

func TestDSLCatalogWrite_AutoFillsOwner(t *testing.T) {
	s, ctx, cat := dslRLSTestServer(t)
	userCtx := auth.ContextWithUser(ctx, &auth.User{Login: "u", Roles: []*auth.Role{{
		Permissions: auth.Permission{
			Catalogs: map[string][]string{cat.Name: {"read", "write"}},
			RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
				cat.Name: {
					"read":  {Field: "Owner", Op: "eq", Value: auth.RowValue{User: "login"}},
					"write": {SameAs: "read"},
				},
			}},
		},
	}}})

	result := runDSLRowAccessFunc(t, s, userCtx, `Функция Проверка() Экспорт
  Т = Справочники.Товар.Создать();
  Т.Наименование = "Created";
  С = Т.Записать();
  Возврат ЗначениеРеквизитаОбъекта(С, "Owner");
КонецФункции`)
	if result != "u" {
		t.Fatalf("auto-filled owner = %v, want u", result)
	}
}

func TestDSLQuery_RowAccessReferencePredicate(t *testing.T) {
	client := &metadata.Entity{
		Name: "Клиент", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Owner", Type: metadata.FieldTypeString},
		},
	}
	order := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Клиент", Type: metadata.FieldTypeString, RefEntity: client.Name},
		},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, order})
	allowedClient := uuid.New()
	hiddenClient := uuid.New()
	if err := s.store.Upsert(ctx, client.Name, allowedClient, map[string]any{"Наименование": "A", "Owner": "u"}, client); err != nil {
		t.Fatalf("upsert allowed client: %v", err)
	}
	if err := s.store.Upsert(ctx, client.Name, hiddenClient, map[string]any{"Наименование": "B", "Owner": "other"}, client); err != nil {
		t.Fatalf("upsert hidden client: %v", err)
	}
	if err := s.store.Upsert(ctx, order.Name, uuid.New(), map[string]any{"Номер": "1", "Клиент": allowedClient.String()}, order); err != nil {
		t.Fatalf("upsert allowed order: %v", err)
	}
	if err := s.store.Upsert(ctx, order.Name, uuid.New(), map[string]any{"Номер": "2", "Клиент": hiddenClient.String()}, order); err != nil {
		t.Fatalf("upsert hidden order: %v", err)
	}
	userCtx := auth.ContextWithUser(ctx, &auth.User{Login: "u", Roles: []*auth.Role{{
		Permissions: auth.Permission{
			Documents: map[string][]string{order.Name: {"read"}},
			RowAccess: auth.RowAccess{Documents: map[string]auth.RowPolicies{
				order.Name: {"read": {Field: "Клиент.Owner", Op: "eq", Value: auth.RowValue{User: "login"}}},
			}},
		},
	}}})

	result := runDSLRowAccessFunc(t, s, userCtx, `Функция Проверка() Экспорт
  З = Новый Запрос;
  З.Текст = "ВЫБРАТЬ Номер ИЗ Документ.Заказ";
  Р = З.Выполнить();
  Возврат Р.Количество();
КонецФункции`)
	if result != float64(1) {
		t.Fatalf("DSL query row count = %v, want 1", result)
	}
}
