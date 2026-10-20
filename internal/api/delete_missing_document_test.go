package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
)

func TestAPI_DeletePostingDocumentMissing(t *testing.T) {
	for _, version := range []string{"v1", "v2"} {
		t.Run(version, func(t *testing.T) {
			for _, scenario := range []string{"repeat", "unknown", "orphan_movements", "hook_refusal"} {
				t.Run(scenario, func(t *testing.T) {
					doc := &metadata.Entity{
						Name: "УстановкаЦен", Kind: metadata.KindDocument, Posting: true,
						Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
					}
					ir := &metadata.InfoRegister{
						Name: "Цены", Periodic: true,
						Dimensions: []metadata.Field{{Name: "Товар", Type: metadata.FieldTypeString}},
						Resources:  []metadata.Field{{Name: "Цена", Type: metadata.FieldTypeNumber}},
					}
					h, ctx := newAPITestHandler(t, []*metadata.Entity{doc}, nil)
					if err := h.store.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
						t.Fatal(err)
					}
					h.reg.Load(runtime.LoadOptions{
						Entities: []*metadata.Entity{doc}, InfoRegs: []*metadata.InfoRegister{ir},
						Programs: map[string]*ast.Program{doc.Name: mustParseProgram(t, `
Процедура ОбработкаУдаленияПроведения()
  Если ЭтотОбъект.Posted Тогда
    ВызватьИсключение("обработчик увидел проведённый документ");
  КонецЕсли;
  Если ЭтотОбъект.Номер = "запрет" Тогда
    ВызватьИсключение("удаление запрещено обработчиком отмены проведения");
  КонецЕсли;
КонецПроцедуры`)},
					})
					id := uuid.New()
					requests := 1
					if scenario == "repeat" || scenario == "hook_refusal" {
						number := "1"
						if scenario == "hook_refusal" {
							number = "запрет"
						}
						if err := h.store.Upsert(ctx, doc.Name, id, map[string]any{"Номер": number}, doc); err != nil {
							t.Fatal(err)
						}
						if err := h.store.SetPosted(ctx, doc.Name, id, true); err != nil {
							t.Fatal(err)
						}
						if scenario == "repeat" {
							requests = 2
						}
					}
					if scenario != "unknown" {
						period := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
						if err := h.store.WriteInfoMovements(ctx, ir.Name, doc.Name, id, []map[string]any{{
							"Товар": "Молоток", "Цена": float64(100),
						}}, ir, &period); err != nil {
							t.Fatal(err)
						}
					}

					request := newDeletionHTTPRequest(t, h)
					path := "/documents/" + url.PathEscape(doc.Name) + "/" + id.String()
					if version == "v2" {
						path = "/api/v2/document/" + url.PathEscape(doc.Name) + "/" + id.String()
					}
					before, err := h.store.GetByID(ctx, doc.Name, id, doc)
					if scenario == "repeat" || scenario == "hook_refusal" {
						if err != nil {
							t.Fatal(err)
						}
					} else if !storage.IsNotFound(err) {
						t.Fatalf("expected missing document before DELETE: %v", err)
					}
					movementsBefore, err := h.store.InfoRegList(ctx, ir, storage.RegFilter{})
					if err != nil {
						t.Fatal(err)
					}
					for attempt := 1; attempt <= requests; attempt++ {
						w := request(path)
						if scenario == "hook_refusal" {
							if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "удаление запрещено обработчиком отмены проведения") {
								t.Fatalf("hook refusal: expected 409 with hook message, got %d: %s", w.Code, w.Body.String())
							}
						} else if w.Code != http.StatusNoContent {
							t.Fatalf("DELETE #%d: expected 204, got %d: %s", attempt, w.Code, w.Body.String())
						}
					}
					after, err := h.store.GetByID(ctx, doc.Name, id, doc)
					if scenario == "hook_refusal" {
						if err != nil || !reflect.DeepEqual(before, after) {
							t.Fatalf("refused DELETE changed document: before=%v after=%v err=%v", before, after, err)
						}
					} else if !storage.IsNotFound(err) {
						t.Fatalf("document should be absent: %v", err)
					}
					rows, err := h.store.InfoRegList(ctx, ir, storage.RegFilter{})
					if err != nil {
						t.Fatal(err)
					}
					if scenario == "hook_refusal" {
						if len(rows) != 1 || !reflect.DeepEqual(movementsBefore, rows) {
							t.Fatalf("refused DELETE changed movements: before=%v after=%v", movementsBefore, rows)
						}
					} else if len(rows) != 0 {
						t.Fatalf("DELETE left %d orphan movements: %v", len(rows), rows)
					}
				})
			}
		})
	}
}

// Exercise the production router, authentication and frontend entity service.
// The v1 client uses a real session; v2 uses a real integration token.
func newDeletionHTTPRequest(t *testing.T, h *handler) func(string) *httptest.ResponseRecorder {
	t.Helper()
	repo := auth.NewRepo(h.store)
	ctx := t.Context()
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := repo.Create(ctx, "delete-admin", "password123", "", true)
	if err != nil {
		t.Fatal(err)
	}
	session, err := repo.CreateSession(ctx, u.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := repo.CreateAPIToken(ctx, "delete-test", u.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(h.reg, h.store, h.interp, repo, "127.0.0.1", 0, ui.Config{}, nil)
	t.Cleanup(srv.frontend.(*ui.Server).Close)
	return func(path string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodDelete, path, nil)
		if strings.HasPrefix(path, "/api/v2/") {
			req.Header.Set("Authorization", "Bearer "+token)
		} else {
			req.AddCookie(&http.Cookie{Name: "onebase_session", Value: session})
		}
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		return w
	}
}
