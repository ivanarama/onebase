package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/dslvars"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Тесты подтверждают что новый код API действительно закрывает дыры из
// FUNCTIONAL_GAPS секции коммита: OnWrite получает DSL-vars, ТЧ передаются
// через JSON, optimistic locking через If-Match, проведение через /post.

func newAPITestHandler(t *testing.T, entities []*metadata.Entity, programs map[string]*ast.Program) (*handler, context.Context) {
	return newAPITestHandlerWithReports(t, entities, nil, programs)
}

func newAPITestHandlerWithReports(t *testing.T, entities []*metadata.Entity, reports []*reportpkg.Report, programs map[string]*ast.Program) (*handler, context.Context) {
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
	registry.Load(runtime.LoadOptions{Entities: entities, Reports: reports, Programs: programs})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	// API-вариант BuildVars: используем dslvars.Common — даёт OnWrite доступ
	// к Перечислениям/Константам/Запрос/Предопределённым + HTTP/Email/Движения.
	// Полный UI-вариант (locks/users/Сообщить-store/Документы) для API избыточен.
	buildVars := func(c context.Context, mc *runtime.MovementsCollector, msgs *[]string) map[string]any {
		vars := dslvars.Common{Ctx: c, Reg: registry, Store: db, Movements: mc}.Build()
		// Минимальная реализация Сообщить — кладёт в msgs slice. Для теста этого достаточно.
		vars["Сообщить"] = interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
			if len(args) > 0 && msgs != nil {
				*msgs = append(*msgs, toString(args[0]))
			}
			return nil, nil
		})
		vars["Message"] = vars["Сообщить"]
		return vars
	}

	svc := &entityservice.Service{
		Store:     db,
		Reg:       registry,
		Interp:    interp,
		BuildVars: buildVars,
	}
	h := &handler{reg: registry, store: db, interp: interp, entitySvc: svc}
	return h, ctx
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func mustParseProgram(t *testing.T, src string) *ast.Program {
	t.Helper()
	l := lexer.New(src, "test.os")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	return prog
}

// reqWithEntity создаёт chi-aware request с URL-параметрами {entity, id}.
func reqWithEntity(method, target string, body []byte, params map[string]string, headers map[string]string) *http.Request {
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, target, reader)
	r.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func withUser(r *http.Request, u *auth.User) *http.Request {
	return r.WithContext(auth.ContextWithUser(r.Context(), u))
}

func apiUser(login string, permissions auth.Permission) *auth.User {
	return &auth.User{
		ID:    "u-" + login,
		Login: login,
		Roles: []*auth.Role{{
			Name:        "role-" + login,
			Permissions: permissions,
		}},
	}
}

// БЫЛО (до миграции): POST /catalogs/X с OnWrite, который зовёт Сообщить(),
// падал с 422 «неизвестная функция Сообщить». СТАЛО: success + сообщение
// возвращается в JSON.
func TestAPI_Create_OnWriteHasDSLVars(t *testing.T) {
	cat := &metadata.Entity{
		Name: "Товар",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	prog := mustParseProgram(t, `Процедура OnWrite()
  Сообщить("сохранён: " + ЭтотОбъект.Наименование);
КонецПроцедуры`)
	h, _ := newAPITestHandler(t, []*metadata.Entity{cat}, map[string]*ast.Program{"Товар": prog})

	body := []byte(`{"Наименование": "Молоток"}`)
	r := reqWithEntity("POST", "/catalogs/Товар", body, map[string]string{"entity": "Товар"}, nil)
	w := httptest.NewRecorder()
	h.createObject(metadata.KindCatalog).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		ID       string   `json:"id"`
		Messages []string `json:"messages"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID == "" {
		t.Error("ID не вернулся")
	}
	if len(resp.Messages) != 1 || !strings.Contains(resp.Messages[0], "Молоток") {
		t.Errorf("Сообщения не вернулись: %v", resp.Messages)
	}
}

func TestAPI_RBAC_DeniesCreateWithoutWrite(t *testing.T) {
	cat := &metadata.Entity{
		Name: "Товар",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	h, _ := newAPITestHandler(t, []*metadata.Entity{cat}, nil)
	user := &auth.User{
		ID:    "u1",
		Login: "reader",
		Roles: []*auth.Role{{
			Name: "Читатель",
			Permissions: auth.Permission{
				Catalogs: map[string][]string{"Товар": {"read"}},
			},
		}},
	}

	body := []byte(`{"Наименование": "Молоток"}`)
	r := reqWithEntity("POST", "/catalogs/Товар", body, map[string]string{"entity": "Товар"}, nil)
	r = withUser(r, user)
	w := httptest.NewRecorder()
	h.createObject(metadata.KindCatalog).ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_RBAC_DeniesListAndGetWithoutRead(t *testing.T) {
	cat := &metadata.Entity{
		Name: "Товар",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	h, ctx := newAPITestHandler(t, []*metadata.Entity{cat}, nil)
	id := uuid.New()
	if err := h.store.Upsert(ctx, "Товар", id, map[string]any{"Наименование": "Молоток"}, cat); err != nil {
		t.Fatal(err)
	}
	user := apiUser("writer", auth.Permission{
		Catalogs: map[string][]string{"Товар": {"write"}},
	})

	listReq := reqWithEntity("GET", "/catalogs/Товар", nil, map[string]string{"entity": "Товар"}, nil)
	listReq = withUser(listReq, user)
	listRec := httptest.NewRecorder()
	h.listObjects(metadata.KindCatalog).ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusForbidden {
		t.Fatalf("list: expected 403, got %d: %s", listRec.Code, listRec.Body.String())
	}

	getReq := reqWithEntity("GET", "/catalogs/Товар/"+id.String(), nil,
		map[string]string{"entity": "Товар", "id": id.String()}, nil)
	getReq = withUser(getReq, user)
	getRec := httptest.NewRecorder()
	h.getObject(metadata.KindCatalog).ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusForbidden {
		t.Fatalf("get: expected 403, got %d: %s", getRec.Code, getRec.Body.String())
	}
}

func TestAPI_RBAC_UpdatePostActionRequiresPost(t *testing.T) {
	doc := &metadata.Entity{
		Name:    "Поступление",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields:  []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	h, ctx := newAPITestHandler(t, []*metadata.Entity{doc}, nil)
	id := uuid.New()
	if err := h.store.Upsert(ctx, "Поступление", id, map[string]any{"Номер": "1"}, doc); err != nil {
		t.Fatal(err)
	}
	user := &auth.User{
		ID:    "u1",
		Login: "writer",
		Roles: []*auth.Role{{
			Name: "Писатель",
			Permissions: auth.Permission{
				Documents: map[string][]string{"Поступление": {"read", "write"}},
			},
		}},
	}

	body := []byte(`{"Номер":"2","__action":"post"}`)
	r := reqWithEntity("PUT", "/documents/Поступление/"+id.String(), body,
		map[string]string{"entity": "Поступление", "id": id.String()}, nil)
	r = withUser(r, user)
	w := httptest.NewRecorder()
	h.updateObject(metadata.KindDocument).ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_RBAC_DeniesDeleteWithoutDelete(t *testing.T) {
	cat := &metadata.Entity{
		Name: "Товар",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	h, ctx := newAPITestHandler(t, []*metadata.Entity{cat}, nil)
	id := uuid.New()
	if err := h.store.Upsert(ctx, "Товар", id, map[string]any{"Наименование": "Молоток"}, cat); err != nil {
		t.Fatal(err)
	}
	user := apiUser("writer", auth.Permission{
		Catalogs: map[string][]string{"Товар": {"read", "write"}},
	})

	r := reqWithEntity("DELETE", "/catalogs/Товар/"+id.String(), nil,
		map[string]string{"entity": "Товар", "id": id.String()}, nil)
	r = withUser(r, user)
	w := httptest.NewRecorder()
	h.deleteObject(metadata.KindCatalog).ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := h.store.GetByID(ctx, "Товар", id, cat); err != nil {
		t.Fatalf("object was deleted despite missing delete permission: %v", err)
	}
}

func TestAPI_RBAC_PostDocumentRequiresPost(t *testing.T) {
	doc := &metadata.Entity{
		Name:    "Поступление",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields:  []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	h, ctx := newAPITestHandler(t, []*metadata.Entity{doc}, nil)
	id := uuid.New()
	if err := h.store.Upsert(ctx, "Поступление", id, map[string]any{"Номер": "1"}, doc); err != nil {
		t.Fatal(err)
	}
	user := apiUser("writer", auth.Permission{
		Documents: map[string][]string{"Поступление": {"read", "write"}},
	})

	r := reqWithEntity("POST", "/documents/Поступление/"+id.String()+"/post", nil,
		map[string]string{"entity": "Поступление", "id": id.String()}, nil)
	r = withUser(r, user)
	w := httptest.NewRecorder()
	h.postDocument().ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_RBAC_PostDocumentWithBodyRequiresWrite(t *testing.T) {
	doc := &metadata.Entity{
		Name:    "Поступление",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields:  []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	h, ctx := newAPITestHandler(t, []*metadata.Entity{doc}, nil)
	id := uuid.New()
	if err := h.store.Upsert(ctx, "Поступление", id, map[string]any{"Номер": "1"}, doc); err != nil {
		t.Fatal(err)
	}
	user := apiUser("poster", auth.Permission{
		Documents: map[string][]string{"Поступление": {"read", "post"}},
	})

	r := reqWithEntity("POST", "/documents/Поступление/"+id.String()+"/post", []byte(`{"Номер":"2"}`),
		map[string]string{"entity": "Поступление", "id": id.String()}, nil)
	r = withUser(r, user)
	w := httptest.NewRecorder()
	h.postDocument().ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_List_DefaultLimitAndHeaders(t *testing.T) {
	cat := &metadata.Entity{
		Name: "Товар",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	h, ctx := newAPITestHandler(t, []*metadata.Entity{cat}, nil)
	for i := 0; i < restDefaultLimit+5; i++ {
		id := uuid.New()
		if err := h.store.Upsert(ctx, "Товар", id, map[string]any{"Наименование": "Товар"}, cat); err != nil {
			t.Fatal(err)
		}
	}

	r := reqWithEntity("GET", "/catalogs/Товар", nil, map[string]string{"entity": "Товар"}, nil)
	w := httptest.NewRecorder()
	h.listObjects(metadata.KindCatalog).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Total-Count"); got != "105" {
		t.Fatalf("X-Total-Count = %q, want 105", got)
	}
	if got := w.Header().Get("X-Limit"); got != "100" {
		t.Fatalf("X-Limit = %q, want 100", got)
	}
	if got := w.Header().Get("X-Offset"); got != "0" {
		t.Fatalf("X-Offset = %q, want 0", got)
	}
	var rows []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != restDefaultLimit {
		t.Fatalf("len(rows) = %d, want %d", len(rows), restDefaultLimit)
	}
}

func TestAPI_List_LimitIsCappedAndOffsetApplied(t *testing.T) {
	cat := &metadata.Entity{
		Name: "Товар",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	h, ctx := newAPITestHandler(t, []*metadata.Entity{cat}, nil)
	for i := 0; i < 3; i++ {
		id := uuid.New()
		if err := h.store.Upsert(ctx, "Товар", id, map[string]any{"Наименование": "Товар"}, cat); err != nil {
			t.Fatal(err)
		}
	}

	r := reqWithEntity("GET", "/catalogs/Товар?limit=5000&offset=1", nil, map[string]string{"entity": "Товар"}, nil)
	w := httptest.NewRecorder()
	h.listObjects(metadata.KindCatalog).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Limit"); got != "1000" {
		t.Fatalf("X-Limit = %q, want 1000", got)
	}
	if got := w.Header().Get("X-Offset"); got != "1" {
		t.Fatalf("X-Offset = %q, want 1", got)
	}
	var rows []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
}

func TestAPI_List_InvalidLimit(t *testing.T) {
	cat := &metadata.Entity{Name: "Товар", Kind: metadata.KindCatalog}
	h, _ := newAPITestHandler(t, []*metadata.Entity{cat}, nil)

	r := reqWithEntity("GET", "/catalogs/Товар?limit=0", nil, map[string]string{"entity": "Товар"}, nil)
	w := httptest.NewRecorder()
	h.listObjects(metadata.KindCatalog).ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPIV2_ListEnvelopePageAndFilter(t *testing.T) {
	cat := &metadata.Entity{
		Name: "Товар",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	h, ctx := newAPITestHandler(t, []*metadata.Entity{cat}, nil)
	for _, name := range []string{"Молоток 1", "Молоток 2", "Гвоздь"} {
		if err := h.store.Upsert(ctx, "Товар", uuid.New(), map[string]any{"Наименование": name}, cat); err != nil {
			t.Fatal(err)
		}
	}

	target := "/api/v2/catalog/Товар?limit=1&page=2&sort=" + url.QueryEscape("Наименование") +
		"&filter%5B" + url.QueryEscape("Наименование") + "%5D=" + url.QueryEscape("Молоток")
	r := reqWithEntity("GET", target, nil, map[string]string{"name": "Товар"}, nil)
	w := httptest.NewRecorder()
	h.listObjectsV2(metadata.KindCatalog).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data []map[string]any `json:"data"`
		Meta restV2Meta       `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1: %#v", len(resp.Data), resp.Data)
	}
	if resp.Data[0]["Наименование"] != "Молоток 2" {
		t.Fatalf("second page row = %#v, want Молоток 2", resp.Data[0])
	}
	if resp.Meta.Total != 2 || resp.Meta.Page != 2 || resp.Meta.Limit != 1 || resp.Meta.TotalPages != 2 {
		t.Fatalf("bad meta: %+v", resp.Meta)
	}
}

func TestAPIV2_CRUDRoundTripEnvelope(t *testing.T) {
	cat := &metadata.Entity{
		Name: "Товар",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	h, ctx := newAPITestHandler(t, []*metadata.Entity{cat}, nil)

	createReq := reqWithEntity("POST", "/api/v2/catalog/Товар", []byte(`{"Наименование":"Молоток"}`), map[string]string{"name": "Товар"}, nil)
	createRec := httptest.NewRecorder()
	h.createObjectV2(metadata.KindCatalog).ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create: expected 200, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Data.ID == "" {
		t.Fatal("create returned empty id")
	}

	getReq := reqWithEntity("GET", "/api/v2/catalog/Товар/"+created.Data.ID, nil, map[string]string{"name": "Товар", "id": created.Data.ID}, nil)
	getRec := httptest.NewRecorder()
	h.getObjectV2(metadata.KindCatalog).ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get: expected 200, got %d: %s", getRec.Code, getRec.Body.String())
	}
	var got struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Data["Наименование"] != "Молоток" {
		t.Fatalf("get data = %#v", got.Data)
	}

	updateReq := reqWithEntity("PUT", "/api/v2/catalog/Товар/"+created.Data.ID, []byte(`{"Наименование":"Дрель"}`), map[string]string{"name": "Товар", "id": created.Data.ID}, nil)
	updateRec := httptest.NewRecorder()
	h.updateObjectV2(metadata.KindCatalog).ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d: %s", updateRec.Code, updateRec.Body.String())
	}

	id := uuid.MustParse(created.Data.ID)
	row, err := h.store.GetByID(ctx, "Товар", id, cat)
	if err != nil {
		t.Fatal(err)
	}
	if row["Наименование"] != "Дрель" {
		t.Fatalf("stored name = %#v", row)
	}

	deleteReq := reqWithEntity("DELETE", "/api/v2/catalog/Товар/"+created.Data.ID, nil, map[string]string{"name": "Товар", "id": created.Data.ID}, nil)
	deleteRec := httptest.NewRecorder()
	h.deleteObjectV2(metadata.KindCatalog).ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d: %s", deleteRec.Code, deleteRec.Body.String())
	}
	if _, err := h.store.GetByID(ctx, "Товар", id, cat); err == nil {
		t.Fatal("deleted object is still readable")
	}
}

func TestAPIV2_RBAC_DeniesListWithoutRead(t *testing.T) {
	cat := &metadata.Entity{
		Name:   "Товар",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	h, ctx := newAPITestHandler(t, []*metadata.Entity{cat}, nil)
	if err := h.store.Upsert(ctx, "Товар", uuid.New(), map[string]any{"Наименование": "Молоток"}, cat); err != nil {
		t.Fatal(err)
	}
	user := apiUser("writer", auth.Permission{
		Catalogs: map[string][]string{"Товар": {"write"}},
	})

	r := reqWithEntity("GET", "/api/v2/catalog/Товар", nil, map[string]string{"name": "Товар"}, nil)
	r = withUser(r, user)
	w := httptest.NewRecorder()
	h.listObjectsV2(metadata.KindCatalog).ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPIV2_UnpostDocument(t *testing.T) {
	doc := &metadata.Entity{
		Name:    "Поступление",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields:  []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	h, ctx := newAPITestHandler(t, []*metadata.Entity{doc}, nil)
	id := uuid.New()
	if err := h.store.Upsert(ctx, "Поступление", id, map[string]any{"Номер": "1"}, doc); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetPosted(ctx, "Поступление", id, true); err != nil {
		t.Fatal(err)
	}

	r := reqWithEntity("POST", "/api/v2/document/Поступление/"+id.String()+"/unpost", nil, map[string]string{"name": "Поступление", "id": id.String()}, nil)
	w := httptest.NewRecorder()
	h.unpostDocumentV2().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	row, err := h.store.GetByID(ctx, "Поступление", id, doc)
	if err != nil {
		t.Fatal(err)
	}
	if row["posted"] != false {
		t.Fatalf("posted = %#v, want false; row=%#v", row["posted"], row)
	}
}

func TestAPIV2_OpenAPIIncludesPathsAndSchemas(t *testing.T) {
	cat := &metadata.Entity{
		Name:   "Товар",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	doc := &metadata.Entity{
		Name:    "Поступление",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields:  []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	rep := &reportpkg.Report{
		Name:  "Остатки",
		Query: "ВЫБРАТЬ 1 КАК Количество",
		Params: []reportpkg.Param{
			{Name: "НаДату", Type: "date"},
		},
	}
	spec := buildOpenAPIV2([]*metadata.Entity{cat, doc}, []*reportpkg.Report{rep})

	if spec["openapi"] != "3.0.3" {
		t.Fatalf("openapi = %#v", spec["openapi"])
	}
	paths := spec["paths"].(map[string]any)
	if _, ok := paths["/api/v2/catalog/{name}"]; !ok {
		t.Fatalf("missing catalog path: %#v", paths)
	}
	if _, ok := paths["/api/v2/document/{name}/{id}/unpost"]; !ok {
		t.Fatalf("missing unpost path: %#v", paths)
	}
	reportPath := paths["/api/v2/report/{name}"].(map[string]any)
	reportGet := reportPath["get"].(map[string]any)
	if reportGet["operationId"] != "runReport" {
		t.Fatalf("report operationId = %#v", reportGet["operationId"])
	}
	reportParams := reportGet["parameters"].([]any)
	if !openAPIHasQueryParam(reportParams, "composition") || !openAPIHasQueryParam(reportParams, "variant") {
		t.Fatalf("report parameters must include composition and variant: %#v", reportParams)
	}
	components := spec["components"].(map[string]any)
	securitySchemes := components["securitySchemes"].(map[string]any)
	if _, ok := securitySchemes["bearerAuth"]; !ok {
		t.Fatalf("missing bearerAuth security scheme: %#v", securitySchemes)
	}
	schemas := components["schemas"].(map[string]any)
	schema := schemas["catalog_Товар"].(map[string]any)
	props := schema["properties"].(map[string]any)
	if _, ok := props["Наименование"]; !ok {
		t.Fatalf("catalog schema lacks field: %#v", props)
	}
	reportSchema := schemas["report_Остатки"].(map[string]any)
	reportProps := reportSchema["properties"].(map[string]any)
	param := reportProps["НаДату"].(map[string]any)
	if param["format"] != "date" {
		t.Fatalf("report param schema = %#v", param)
	}
	if _, err := json.Marshal(spec); err != nil {
		t.Fatalf("openapi spec must be JSON-serializable: %v", err)
	}
}

func openAPIHasQueryParam(params []any, name string) bool {
	for _, p := range params {
		pm, ok := p.(map[string]any)
		if ok && pm["name"] == name && pm["in"] == "query" {
			return true
		}
	}
	return false
}

func TestAPIV2_ReportRunsQueryEnvelope(t *testing.T) {
	cat := &metadata.Entity{
		Name: "Товар",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	rep := &reportpkg.Report{
		Name:  "СписокТоваров",
		Query: `ВЫБРАТЬ Наименование ИЗ Справочник.Товар УПОРЯДОЧИТЬ ПО Наименование`,
	}
	h, ctx := newAPITestHandlerWithReports(t, []*metadata.Entity{cat}, []*reportpkg.Report{rep}, nil)
	for _, name := range []string{"Дрель", "Молоток"} {
		if err := h.store.Upsert(ctx, "Товар", uuid.New(), map[string]any{"Наименование": name}, cat); err != nil {
			t.Fatal(err)
		}
	}

	r := reqWithEntity("GET", "/api/v2/report/СписокТоваров?limit=1", nil, map[string]string{"name": "СписокТоваров"}, nil)
	w := httptest.NewRecorder()
	h.runReportV2().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data []map[string]any `json:"data"`
		Meta restV2Meta       `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 || resp.Data[0]["наименование"] != "Дрель" {
		t.Fatalf("bad report data: %#v", resp.Data)
	}
	if resp.Meta.Total != 1 || resp.Meta.Limit != 1 || !resp.Meta.Truncated {
		t.Fatalf("bad report meta: %+v", resp.Meta)
	}
	if len(resp.Meta.Columns) != 1 || resp.Meta.Columns[0] != "наименование" {
		t.Fatalf("columns = %#v", resp.Meta.Columns)
	}
}

func TestAPIV2_ReportParamsAndRBAC(t *testing.T) {
	rep := &reportpkg.Report{
		Name: "Сумма",
		Params: []reportpkg.Param{
			{Name: "Значение", Type: "number"},
		},
		Query: `ВЫБРАТЬ &Значение КАК Значение`,
	}
	h, _ := newAPITestHandlerWithReports(t, nil, []*reportpkg.Report{rep}, nil)
	user := apiUser("reader", auth.Permission{
		Reports: map[string][]string{"Другой": {"run"}},
	})

	deniedReq := reqWithEntity("GET", "/api/v2/report/Сумма?Значение=7", nil, map[string]string{"name": "Сумма"}, nil)
	deniedReq = withUser(deniedReq, user)
	deniedRec := httptest.NewRecorder()
	h.runReportV2().ServeHTTP(deniedRec, deniedReq)
	if deniedRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", deniedRec.Code, deniedRec.Body.String())
	}

	allowedReq := reqWithEntity("GET", "/api/v2/report/Сумма?Значение=7", nil, map[string]string{"name": "Сумма"}, nil)
	allowedReq = withUser(allowedReq, apiUser("runner", auth.Permission{
		Reports: map[string][]string{"Сумма": {"run"}},
	}))
	allowedRec := httptest.NewRecorder()
	h.runReportV2().ServeHTTP(allowedRec, allowedReq)
	if allowedRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", allowedRec.Code, allowedRec.Body.String())
	}
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(allowedRec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 || resp.Data[0]["значение"] == nil {
		t.Fatalf("bad report data: %#v", resp.Data)
	}
}

func TestAPIV2_ReportUsesBearerTokenRBAC(t *testing.T) {
	rep := &reportpkg.Report{
		Name: "Сумма",
		Params: []reportpkg.Param{
			{Name: "Значение", Type: "number"},
		},
		Query: `ВЫБРАТЬ &Значение КАК Значение`,
	}
	h, ctx := newAPITestHandlerWithReports(t, nil, []*reportpkg.Report{rep}, nil)
	authRepo := auth.NewRepo(h.store)
	if err := authRepo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	allowedUser, _ := authRepo.Create(ctx, "runner", "pass", "", false)
	deniedUser, _ := authRepo.Create(ctx, "reader", "pass", "", false)
	role := []*auth.Role{{
		Name: "report-runner",
		Permissions: auth.Permission{
			Reports: map[string][]string{"Сумма": {"run"}},
		},
	}}
	if err := authRepo.SyncRoles(ctx, role); err != nil {
		t.Fatal(err)
	}
	if err := authRepo.AssignRole(ctx, allowedUser.ID, role[0].ID); err != nil {
		t.Fatal(err)
	}
	_, allowedRaw, err := authRepo.CreateAPIToken(ctx, "allowed", allowedUser.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, deniedRaw, err := authRepo.CreateAPIToken(ctx, "denied", deniedUser.ID, nil)
	if err != nil {
		t.Fatal(err)
	}

	router := chi.NewRouter()
	router.Use(authRepo.APITokenOrSessionMiddleware)
	h.mountV2(router)
	target := "/api/v2/report/" + url.PathEscape("Сумма") + "?Значение=7"

	noAuth := httptest.NewRecorder()
	router.ServeHTTP(noAuth, httptest.NewRequest(http.MethodGet, target, nil))
	if noAuth.Code != http.StatusUnauthorized {
		t.Fatalf("no auth expected 401, got %d: %s", noAuth.Code, noAuth.Body.String())
	}

	deniedReq := httptest.NewRequest(http.MethodGet, target, nil)
	deniedReq.Header.Set("Authorization", "Bearer "+deniedRaw)
	deniedRec := httptest.NewRecorder()
	router.ServeHTTP(deniedRec, deniedReq)
	if deniedRec.Code != http.StatusForbidden {
		t.Fatalf("denied token expected 403, got %d: %s", deniedRec.Code, deniedRec.Body.String())
	}

	allowedReq := httptest.NewRequest(http.MethodGet, target, nil)
	allowedReq.Header.Set("Authorization", "Bearer "+allowedRaw)
	allowedRec := httptest.NewRecorder()
	router.ServeHTTP(allowedRec, allowedReq)
	if allowedRec.Code != http.StatusOK {
		t.Fatalf("allowed token expected 200, got %d: %s", allowedRec.Code, allowedRec.Body.String())
	}
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(allowedRec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 || resp.Data[0]["значение"] == nil {
		t.Fatalf("bad report data: %#v", resp.Data)
	}
}

func TestAPIV2_ReportCompositionEnvelope(t *testing.T) {
	cat := &metadata.Entity{
		Name: "Товар",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Категория", Type: metadata.FieldTypeString},
			{Name: "Количество", Type: metadata.FieldTypeNumber},
		},
	}
	rep := &reportpkg.Report{
		Name:  "Свод",
		Query: `ВЫБРАТЬ Категория, Количество ИЗ Справочник.Товар УПОРЯДОЧИТЬ ПО Категория`,
		Composition: &reportpkg.Composition{
			Groupings: []string{"Категория"},
			Measures:  []reportpkg.Measure{{Field: "Количество", Agg: "sum"}},
			Detail:    true,
		},
	}
	h, ctx := newAPITestHandlerWithReports(t, []*metadata.Entity{cat}, []*reportpkg.Report{rep}, nil)
	for _, row := range []map[string]any{
		{"Категория": "Инструмент", "Количество": 2},
		{"Категория": "Инструмент", "Количество": 3},
	} {
		if err := h.store.Upsert(ctx, "Товар", uuid.New(), row, cat); err != nil {
			t.Fatal(err)
		}
	}

	r := reqWithEntity("GET", "/api/v2/report/Свод?composition=1", nil, map[string]string{"name": "Свод"}, nil)
	w := httptest.NewRecorder()
	h.runReportV2().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Kind   string `json:"kind"`
			Result struct {
				Groups []struct {
					Field     string         `json:"field"`
					Key       any            `json:"key"`
					Subtotals map[string]any `json:"subtotals"`
					Details   []any          `json:"details"`
				} `json:"groups"`
				RowCount int `json:"row_count"`
			} `json:"result"`
		} `json:"data"`
		Meta restV2Meta `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Kind != "tree" || !resp.Meta.Composed || resp.Meta.Kind != "tree" {
		t.Fatalf("bad composition markers: data=%+v meta=%+v", resp.Data, resp.Meta)
	}
	if len(resp.Data.Result.Groups) != 1 || resp.Data.Result.Groups[0].Field != "Категория" || resp.Data.Result.Groups[0].Key != "Инструмент" {
		t.Fatalf("bad composition groups: %+v", resp.Data.Result.Groups)
	}
	if resp.Data.Result.RowCount != 2 || len(resp.Data.Result.Groups[0].Details) != 2 {
		t.Fatalf("bad composition row/detail counts: %+v", resp.Data.Result)
	}
}

// БЫЛО: ТЧ через API передать было нельзя — поле игнорировалось.
// СТАЛО: __tableparts в JSON → ТЧ реально сохраняются.
func TestAPI_Create_WithTableParts(t *testing.T) {
	doc := &metadata.Entity{
		Name: "Поступление",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
		},
		TableParts: []metadata.TablePart{
			{Name: "Товары", Fields: []metadata.Field{
				{Name: "Номенклатура", Type: metadata.FieldTypeString},
				{Name: "Количество", Type: metadata.FieldTypeNumber},
			}},
		},
	}
	h, ctx := newAPITestHandler(t, []*metadata.Entity{doc}, nil)

	body := []byte(`{"Номер":"1","__tableparts":{"Товары":[{"Номенклатура":"Гвоздь","Количество":100}]}}`)
	r := reqWithEntity("POST", "/documents/Поступление", body, map[string]string{"entity": "Поступление"}, nil)
	w := httptest.NewRecorder()
	h.createObject(metadata.KindDocument).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		ID string `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	docID, _ := uuid.Parse(resp.ID)

	tpRows, err := h.store.GetTablePartRows(ctx, "Поступление", "Товары", docID, doc.TableParts[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(tpRows) != 1 {
		t.Fatalf("ожидалась 1 строка ТЧ, получено %d: %v", len(tpRows), tpRows)
	}
	nom, _ := tpRows[0]["Номенклатура"].(string)
	if nom == "" {
		nom, _ = tpRows[0]["номенклатура"].(string)
	}
	if nom != "Гвоздь" {
		t.Errorf("Номенклатура = %q, ожидался «Гвоздь»", nom)
	}
}

// БЫЛО: lost updates через API — два клиента POST одинаковым PUT'ом,
// последний выигрывал тихо. СТАЛО: If-Match со stale-версией → 409.
func TestAPI_Update_IfMatchVersionConflict(t *testing.T) {
	cat := &metadata.Entity{
		Name: "Товар",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	h, ctx := newAPITestHandler(t, []*metadata.Entity{cat}, nil)

	id := uuid.New()
	if err := h.store.Upsert(ctx, "Товар", id, map[string]any{"Наименование": "v1"}, cat); err != nil {
		t.Fatal(err)
	}
	// Имитируем чужое обновление: версия в БД продвинулась.
	if err := h.store.UpsertVersioned(ctx, "Товар", id, map[string]any{"Наименование": "v2"}, cat, nil); err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"Наименование":"my-edit"}`)
	// Клиент шлёт If-Match с устаревшей версией 0
	r := reqWithEntity("PUT", "/catalogs/Товар/"+id.String(), body,
		map[string]string{"entity": "Товар", "id": id.String()},
		map[string]string{"If-Match": "0"})
	w := httptest.NewRecorder()
	h.updateObject(metadata.KindCatalog).ServeHTTP(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d: %s", w.Code, w.Body.String())
	}
}

// БЫЛО: провести документ через REST было нельзя (только UI-кнопка).
// СТАЛО: POST /documents/{name}/{id}/post запускает OnPost и пишет движения.
func TestAPI_PostDocument_WritesMovements(t *testing.T) {
	doc := &metadata.Entity{
		Name:    "Поступление",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
		},
	}
	reg := &metadata.Register{
		Name:       "Остатки",
		Dimensions: []metadata.Field{{Name: "Товар", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	// OnPost добавляет приход на склад.
	prog := mustParseProgram(t, `Процедура ОбработкаПроведения()
  Дв = Движения.Остатки.Добавить();
  Дв.ВидДвижения = "Приход";
  Дв.Товар = "Молоток";
  Дв.Количество = 5;
КонецПроцедуры`)

	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{doc},
		Registers: []*metadata.Register{reg},
		Programs:  map[string]*ast.Program{"Поступление": prog},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	buildVars := func(c context.Context, mc *runtime.MovementsCollector, msgs *[]string) map[string]any {
		return dslvars.Common{Ctx: c, Reg: registry, Store: db, Movements: mc}.Build()
	}
	svc := &entityservice.Service{Store: db, Reg: registry, Interp: interp, BuildVars: buildVars}
	h := &handler{reg: registry, store: db, interp: interp, entitySvc: svc}

	// Сначала создаём документ (без проведения).
	docID := uuid.New()
	if err := db.Upsert(ctx, "Поступление", docID, map[string]any{"Номер": "1"}, doc); err != nil {
		t.Fatal(err)
	}

	// Затем POST /post — должно провестись и записать движения.
	r := reqWithEntity("POST", "/documents/Поступление/"+docID.String()+"/post", nil,
		map[string]string{"entity": "Поступление", "id": docID.String()}, nil)
	w := httptest.NewRecorder()
	h.postDocument().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Проверим что движения реально записались.
	rows, err := db.GetMovements(ctx, "Остатки", reg, storage.RegFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("ожидалось 1 движение, получено %d: %v", len(rows), rows)
	}
}

// Регресс (H1): POST /documents/{e}/{id}/post с ПУСТЫМ телом не должен затирать
// табличные части документа, а OnPost обязан видеть строки ТЧ (иначе движения
// считаются по пустой this.Товары). Раньше пустое тело → tpRows=nil → Save
// перезаписывал ТЧ пустым набором и движения выходили нулевыми.
func TestAPI_PostDocument_EmptyBody_KeepsTableParts(t *testing.T) {
	doc := &metadata.Entity{
		Name:    "Поступление",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields:  []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{
			{Name: "Товары", Fields: []metadata.Field{
				{Name: "Номенклатура", Type: metadata.FieldTypeString},
				{Name: "Количество", Type: metadata.FieldTypeNumber},
			}},
		},
	}
	reg := &metadata.Register{
		Name:       "Остатки",
		Dimensions: []metadata.Field{{Name: "Номенклатура", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	// OnPost считает движения по строкам ТЧ — если ТЧ пуста, движений не будет.
	prog := mustParseProgram(t, `Процедура ОбработкаПроведения()
  Для Каждого Стр Из ЭтотОбъект.Товары Цикл
    Дв = Движения.Остатки.Добавить();
    Дв.ВидДвижения = "Приход";
    Дв.Номенклатура = Стр.Номенклатура;
    Дв.Количество = Стр.Количество;
  КонецЦикла;
КонецПроцедуры`)

	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{doc},
		Registers: []*metadata.Register{reg},
		Programs:  map[string]*ast.Program{"Поступление": prog},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	buildVars := func(c context.Context, mc *runtime.MovementsCollector, msgs *[]string) map[string]any {
		return dslvars.Common{Ctx: c, Reg: registry, Store: db, Movements: mc}.Build()
	}
	svc := &entityservice.Service{Store: db, Reg: registry, Interp: interp, BuildVars: buildVars}
	h := &handler{reg: registry, store: db, interp: interp, entitySvc: svc}

	// Создаём документ с одной строкой ТЧ через API (__tableparts).
	body := []byte(`{"Номер":"1","__tableparts":{"Товары":[{"Номенклатура":"Гвоздь","Количество":7}]}}`)
	rc := reqWithEntity("POST", "/documents/Поступление", body, map[string]string{"entity": "Поступление"}, nil)
	wc := httptest.NewRecorder()
	h.createObject(metadata.KindDocument).ServeHTTP(wc, rc)
	if wc.Code != http.StatusOK {
		t.Fatalf("create: ожидалось 200, получено %d: %s", wc.Code, wc.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(wc.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	docID := uuid.MustParse(created.ID)

	// Проводим документ ПУСТЫМ телом.
	rp := reqWithEntity("POST", "/documents/Поступление/"+docID.String()+"/post", nil,
		map[string]string{"entity": "Поступление", "id": docID.String()}, nil)
	wp := httptest.NewRecorder()
	h.postDocument().ServeHTTP(wp, rp)
	if wp.Code != http.StatusOK {
		t.Fatalf("post: ожидалось 200, получено %d: %s", wp.Code, wp.Body.String())
	}

	// 1) Строки ТЧ должны сохраниться, а не обнулиться.
	tpRows, err := db.GetTablePartRows(ctx, "Поступление", "Товары", docID, doc.TableParts[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(tpRows) != 1 {
		t.Fatalf("ТЧ затёрта: ожидалась 1 строка, получено %d: %v", len(tpRows), tpRows)
	}

	// 2) OnPost увидел строку ТЧ → ровно одно движение.
	movs, err := db.GetMovements(ctx, "Остатки", reg, storage.RegFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(movs) != 1 {
		t.Fatalf("ожидалось 1 движение из ТЧ, получено %d: %v", len(movs), movs)
	}
}
