package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Тесты-страховка для submit/submitEdit перед консолидацией. Покрывают:
// 1) создание справочника (submit happy path)
// 2) обновление существующего (submitEdit happy path)
// 3) оптимистический конфликт версий (submitEdit error path → redirect)
// 4) авто-нумерация при создании документа (submit-only поведение)
//
// Тесты НЕ покрывают error-rendering (нужен полный template-bootstrap), но
// этого достаточно чтобы рефакторинг не сломал главные пути сохранения.

func newSubmitTestServer(t *testing.T, entities []*metadata.Entity) (*Server, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: entities})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	s := &Server{
		store:       db,
		reg:         registry,
		interp:      interp,
		lockMgr:     runtime.NewLockManager(),
		messages:    NewMessageStore(),
		aiChatLimit: newAIWindowLimiter(1000, time.Minute), // в тестах лимит не мешает
	}
	s.entitySvc = &entityservice.Service{
		Store:        db,
		Reg:          registry,
		Interp:       interp,
		PrepareHook:  s.enrichHeaderRefs,
		EnrichTPRows: s.enrichTPRowsWithRefs,
		BuildVars:    s.buildDSLVarsWithMessages,
		MakeThis: func(ctx context.Context, obj *runtime.Object, e *metadata.Entity) interpreter.This {
			return s.newFormObjectThis(ctx, obj, e, nil)
		},
	}
	return s, ctx
}

// reqWithChi оборачивает request в chi route context с заданными URL-параметрами.
func reqWithChi(method, target string, body url.Values, params map[string]string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestSubmit_NewCatalog_Saved(t *testing.T) {
	cat := &metadata.Entity{
		Name: "Номенклатура",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})

	form := url.Values{"Наименование": {"Тумбочка"}}
	r := reqWithChi("POST", "/ui/catalog/Номенклатура/new", form, map[string]string{"entity": "Номенклатура"})
	w := httptest.NewRecorder()
	s.submit(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("ожидался 303, получен %d: %s", w.Code, w.Body.String())
	}

	// Проверим что запись реально создалась.
	rows, err := s.store.List(ctx, "Номенклатура", cat, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("ожидалась 1 запись, получено %d", len(rows))
	}
	// Ключ может быть в lowercase (Object.Set нормализует) либо в PascalCase
	// (если submit положил напрямую). Проверим оба варианта.
	name, _ := rows[0]["Наименование"].(string)
	if name == "" {
		name, _ = rows[0]["наименование"].(string)
	}
	if name != "Тумбочка" {
		t.Errorf("Наименование = %q, ожидалось «Тумбочка». Все поля: %v", name, rows[0])
	}
}

func TestSubmitEdit_UpdatesExisting(t *testing.T) {
	cat := &metadata.Entity{
		Name: "Номенклатура",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})

	// Создадим запись напрямую через store, чтобы тест submitEdit не зависел от submit.
	id := uuid.New()
	if err := s.store.Upsert(ctx, "Номенклатура", id, map[string]any{"Наименование": "Старое"}, cat); err != nil {
		t.Fatal(err)
	}

	form := url.Values{"Наименование": {"Новое"}}
	r := reqWithChi("POST", "/ui/catalog/Номенклатура/"+id.String(), form, map[string]string{
		"entity": "Номенклатура",
		"id":     id.String(),
	})
	w := httptest.NewRecorder()
	s.submitEdit(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("ожидался 303, получен %d: %s", w.Code, w.Body.String())
	}

	rec, err := s.store.GetByID(ctx, "Номенклатура", id, cat)
	if err != nil {
		t.Fatal(err)
	}
	name, _ := rec["Наименование"].(string)
	if name == "" {
		name, _ = rec["наименование"].(string)
	}
	if name != "Новое" {
		t.Errorf("Наименование после update = %q, ожидалось «Новое». Запись: %v", name, rec)
	}
}

func TestSubmitEdit_VersionConflict_RedirectsToConflict(t *testing.T) {
	cat := &metadata.Entity{
		Name: "Товар",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})

	id := uuid.New()
	if err := s.store.Upsert(ctx, "Товар", id, map[string]any{"Наименование": "v1"}, cat); err != nil {
		t.Fatal(err)
	}
	// Имитируем чужое обновление между чтением и записью — версия в БД продвинулась.
	if err := s.store.UpsertVersioned(ctx, "Товар", id, map[string]any{"Наименование": "v2"}, cat, nil); err != nil {
		t.Fatal(err)
	}

	// Клиент шлёт изменения с устаревшим _version=0.
	form := url.Values{
		"Наименование": {"my-edit"},
		"_version":     {"0"},
	}
	r := reqWithChi("POST", "/ui/catalog/Товар/"+id.String(), form, map[string]string{
		"entity": "Товар",
		"id":     id.String(),
	})
	w := httptest.NewRecorder()
	s.submitEdit(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("ожидался 303 (redirect на ?conflict=1), получен %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "conflict=1") {
		t.Errorf("Location = %q, ожидался URL с conflict=1", loc)
	}

	// Запись в БД должна остаться v2 — изменения клиента не применились.
	rec, _ := s.store.GetByID(ctx, "Товар", id, cat)
	name, _ := rec["Наименование"].(string)
	if name == "" {
		name, _ = rec["наименование"].(string)
	}
	if name != "v2" {
		t.Errorf("после конфликта в БД = %q, ожидалось «v2» (изменения клиента не должны были применяться)", name)
	}
}

func TestSubmit_NewDocument_AutoNumber(t *testing.T) {
	doc := &metadata.Entity{
		Name: "Заявка",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
		},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{doc})

	form := url.Values{} // Номер пустой — должен сгенерироваться
	r := reqWithChi("POST", "/ui/document/Заявка/new", form, map[string]string{"entity": "Заявка"})
	w := httptest.NewRecorder()
	s.submit(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("ожидался 303, получен %d: %s", w.Code, w.Body.String())
	}

	rows, err := s.store.List(ctx, "Заявка", doc, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("ожидалась 1 запись, получено %d", len(rows))
	}
	num, _ := rows[0]["Номер"].(string)
	if num == "" {
		num, _ = rows[0]["номер"].(string)
	}
	if num == "" {
		t.Errorf("Номер пустой — auto-number не сработал. Запись: %v", rows[0])
	}
}
