package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
)

// ctxSource — минимальный docsCtxSource для DSL-прокси в тестах.
type ctxSource struct{ ctx context.Context }

func (c ctxSource) Ctx() context.Context { return c.ctx }

// errFromPanic превращает RaiseUserError интерпретатора в обычную ошибку.
func errFromPanic(r any) error {
	if err, ok := r.(error); ok {
		return err
	}
	return fmt.Errorf("%v", r)
}

func dslMaskEntities() (*metadata.Entity, *metadata.Entity) {
	client := &metadata.Entity{
		Name: "Клиент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Телефон", Type: metadata.FieldTypeString},
		},
	}
	order := &metadata.Entity{
		Name: "Заявка",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Телефон", Type: metadata.FieldTypeString},
			{Name: "Клиент", Type: "reference:Клиент", RefEntity: "Клиент"},
		},
	}
	return client, order
}

// Объект, прочитанный из БД через DSL (Ссылка.ПолучитьОбъект), обязан отдавать
// защищённый реквизит замаскированным — иначе обработка становится обходом
// полевой политики (план 88E).
func TestDSL_CatalogObjectGetIsMasked(t *testing.T) {
	client, order := dslMaskEntities()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, order})
	id := uuid.New()
	if err := s.store.Upsert(ctx, "Клиент", id, map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455",
	}, client); err != nil {
		t.Fatal(err)
	}
	user := uiMaskUser([]string{"read", "write"}, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}})
	uctx := auth.ContextWithUser(ctx, user)

	obj, err := s.catObjectFactory(ctxSource{uctx}).LoadCatalogObject(client, id.String())
	if err != nil {
		t.Fatal(err)
	}
	w := obj.(*catWriter)
	if got := w.Get("Телефон"); got != "••••••••4455" {
		t.Fatalf("реквизит прочитанного объекта не замаскирован: %v", got)
	}
	if got := w.Get("Наименование"); got != "Иванов" {
		t.Fatalf("незащищённый реквизит изменён: %v", got)
	}

	// Значение, присвоенное самим модулем, читается как есть.
	w.Set("Телефон", "+79990001122")
	if got := w.Get("Телефон"); got != "+79990001122" {
		t.Fatalf("присвоенное модулем значение маскировать нельзя: %v", got)
	}
	// …но записью реальное значение не подменяется: видно только маску —
	// изменить нельзя (тот же контракт, что у формы и REST).
	if _, err := func() (res any, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = errFromPanic(r)
			}
		}()
		return w.CallMethod("записать", nil), nil
	}(); err != nil {
		t.Fatal(err)
	}
	row, err := s.store.GetByID(ctx, "Клиент", id, client)
	if err != nil {
		t.Fatal(err)
	}
	if row["Телефон"] != "+79161234455" {
		t.Fatalf("защищённый реквизит перезаписан из DSL: %v", row["Телефон"])
	}
}

// Объект, созданный самим модулем, не маскируется: значение принадлежит текущей
// операции, а не чужой записи.
func TestDSL_NewCatalogObjectNotMasked(t *testing.T) {
	client, order := dslMaskEntities()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, order})
	user := uiMaskUser([]string{"read", "write"}, auth.FieldPolicies{"Телефон": {Read: "mask_all"}})
	uctx := auth.ContextWithUser(ctx, user)

	obj := s.catObjectFactory(ctxSource{uctx}).NewCatalogObject(client)
	w := obj.(*catWriter)
	w.Set("Телефон", "+79161234455")
	if got := w.Get("Телефон"); got != "+79161234455" {
		t.Fatalf("новый объект маскировать нельзя: %v", got)
	}
}

// Разыменование ссылки на чужую запись (this.Клиент.Телефон) — такой же путь
// чтения, как форма или REST.
func TestDSL_RefAttrDereferenceIsMasked(t *testing.T) {
	client, order := dslMaskEntities()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, order})
	id := uuid.New()
	if err := s.store.Upsert(ctx, "Клиент", id, map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455",
	}, client); err != nil {
		t.Fatal(err)
	}
	user := uiMaskUser([]string{"read"}, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}})
	uctx := auth.ContextWithUser(ctx, user)

	resolver := s.newDSLRefAttrResolver(uctx)
	ref := &interpreter.Ref{UUID: id.String(), Type: "Клиент"}
	v, ok := resolver.ResolveRefAttr(ref, "Телефон")
	if !ok {
		t.Fatal("реквизит по ссылке не разрешён")
	}
	if v != "••••••••4455" {
		t.Fatalf("разыменование отдало реальное значение: %v", v)
	}
}

// Поиск по защищённому реквизиту восстанавливает скрытое значение перебором —
// закрыт целиком, как отбор ГДЕ в запросе.
func TestDSL_FindByMaskedFieldDenied(t *testing.T) {
	client, order := dslMaskEntities()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, order})
	user := uiMaskUser([]string{"read"}, auth.FieldPolicies{"Телефон": {Read: "mask_all"}})
	uctx := auth.ContextWithUser(ctx, user)
	if !s.dslFieldSearchDenied(uctx, client, "Телефон") {
		t.Fatal("поиск по защищённому реквизиту должен быть запрещён")
	}
	if s.dslFieldSearchDenied(uctx, client, "Наименование") {
		t.Fatal("поиск по обычному реквизиту запрещать нельзя")
	}

	docUser := &auth.User{ID: "op", Login: "operator", Roles: []*auth.Role{{
		Name: "Оператор",
		Permissions: auth.Permission{
			Documents: map[string][]string{"Заявка": {"read"}},
			FieldAccess: auth.FieldAccess{
				Documents: map[string]auth.FieldPolicies{"Заявка": {"Телефон": {Read: "mask_all"}}},
			},
		},
	}}}
	docs := newDocsRoot(s, ctxSource{auth.ContextWithUser(ctx, docUser)})
	p, ok := docs.Get("Заявка").(*docProxy)
	if !ok {
		t.Fatalf("Документы.Заявка → %T", docs.Get("Заявка"))
	}
	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = errFromPanic(r)
			}
		}()
		p.CallMethod("найтипореквизиту", []any{"Телефон", "+79161234455"})
		return nil
	}()
	if err == nil || !strings.Contains(err.Error(), "защищён") {
		t.Fatalf("НайтиПоРеквизиту по защищённому полю должен падать, получено: %v", err)
	}
}

// Запрос из модуля не должен быть обходом маски: колонка приходит
// замаскированной, отбор по защищённому полю отклоняется.
func TestDSL_QueryGuardMasksAndDenies(t *testing.T) {
	client, order := dslMaskEntities()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, order})
	if err := s.store.Upsert(ctx, "Клиент", uuid.New(), map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455",
	}, client); err != nil {
		t.Fatal(err)
	}
	user := uiMaskUser([]string{"read"}, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}})
	uctx := auth.ContextWithUser(ctx, user)

	res, err := s.compileQueryWithRowAccess(uctx, `ВЫБРАТЬ Телефон ИЗ Справочник.Клиент`, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.store.QueryAll(uctx, res.SQL, res.Args...)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.dslQueryGuard(uctx, res, rows); err != nil {
		t.Fatal(err)
	}
	if rows[0]["телефон"] != "••••••••4455" {
		t.Fatalf("запрос из модуля отдал реальное значение: %v", rows[0])
	}

	denied, err := s.compileQueryWithRowAccess(uctx, `ВЫБРАТЬ Наименование ИЗ Справочник.Клиент ГДЕ Телефон = "+79161234455"`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.dslQueryGuard(uctx, denied, nil); err == nil {
		t.Fatal("отбор по защищённому полю из модуля должен отклоняться")
	}
}
