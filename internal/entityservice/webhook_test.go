package entityservice

// Тесты диспетчеризации веб-хуков из Service.Save (план 29): события
// catalog.save / document.post уходят наружу после успешной транзакции.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/webhook"
)

type hookSink struct {
	mu     sync.Mutex
	bodies []map[string]any
}

func (h *hookSink) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.bodies)
}

func (h *hookSink) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		h.mu.Lock()
		h.bodies = append(h.bodies, body)
		h.mu.Unlock()
		w.WriteHeader(200)
	}
}

// Третьим значением отдаётся сам *storage.DB: поле Service.Store — узкий порт
// (ports.go), а тесту нужен BeginTx, которого в порту нет и который сервису не
// нужен.
func newWebhookSvc(t *testing.T, entities []*metadata.Entity, hooks []webhook.Config) (*Service, *webhook.Dispatcher, *storage.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "wh.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: entities})
	d := webhook.New(hooks, nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = d.Close(ctx)
	})
	return &Service{Store: db, Reg: registry, Interp: interpreter.New(), Hooks: d}, d, db
}

func TestSave_DispatchesCatalogSaveWebhook(t *testing.T) {
	sink := &hookSink{}
	srv := httptest.NewServer(sink.handler())
	defer srv.Close()

	cat := &metadata.Entity{
		Name: "Товары", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	svc, d, _ := newWebhookSvc(t, []*metadata.Entity{cat}, []webhook.Config{{
		Name: "n8n", On: "catalog.save", URL: srv.URL,
		Body: `{"event":"{{entity}}","name":"{{Наименование}}","id":"{{id}}"}`,
	}})

	id := uuid.New()
	res, err := svc.Save(context.Background(), SaveRequest{
		Entity: cat, ID: id, IsNew: true,
		Fields: map[string]any{"Наименование": "Гвоздь"},
	})
	if err != nil || res.DSLError != "" {
		t.Fatalf("Save: err=%v dsl=%s", err, res.DSLError)
	}
	d.Wait()

	if len(sink.bodies) != 1 {
		t.Fatalf("ожидался 1 веб-хук, получено %d", len(sink.bodies))
	}
	b := sink.bodies[0]
	if b["event"] != "Товары" || b["name"] != "Гвоздь" || b["id"] != id.String() {
		t.Fatalf("тело хука: %+v", b)
	}
}

func TestSave_DispatchesDocumentPostWebhook(t *testing.T) {
	sink := &hookSink{}
	srv := httptest.NewServer(sink.handler())
	defer srv.Close()

	doc := &metadata.Entity{
		Name: "Реализация", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	svc, d, _ := newWebhookSvc(t, []*metadata.Entity{doc}, []webhook.Config{
		{Name: "post-hook", On: "document.post", URL: srv.URL, Body: `{"n":"{{Номер}}"}`},
		{Name: "save-hook", On: "document.save", URL: srv.URL, Body: `{"saved":"{{Номер}}"}`},
	})

	// Проведение → document.post (не document.save)
	res, err := svc.Save(context.Background(), SaveRequest{
		Entity: doc, ID: uuid.New(), IsNew: true, Action: "post",
		Fields: map[string]any{"Номер": "Р-1"},
	})
	if err != nil || res.DSLError != "" {
		t.Fatalf("Save: err=%v dsl=%s", err, res.DSLError)
	}
	d.Wait()

	if len(sink.bodies) != 1 {
		t.Fatalf("ожидался ровно 1 веб-хук (post), получено %d: %+v", len(sink.bodies), sink.bodies)
	}
	if sink.bodies[0]["n"] != "Р-1" {
		t.Fatalf("тело: %+v", sink.bodies[0])
	}
}

// При DSL-ошибке хука сохранения веб-хук НЕ уходит (БД не изменена).
func TestSave_NoWebhookOnDSLError(t *testing.T) {
	sink := &hookSink{}
	srv := httptest.NewServer(sink.handler())
	defer srv.Close()

	cat := &metadata.Entity{
		Name: "Товары", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	svc, d, _ := newWebhookSvc(t, []*metadata.Entity{cat}, []webhook.Config{{
		Name: "x", On: "catalog.save", URL: srv.URL, Body: "{}",
	}})
	// OnWrite с ошибкой — сохранение отменяется бизнес-правилом
	src := `Procedure OnWrite()
  Error("нельзя");
EndProcedure`
	prog, perr := parser.New(lexer.New(src, "товары.os")).ParseProgram()
	if perr != nil {
		t.Fatalf("parse: %v", perr)
	}
	svc.Reg.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{cat},
		Programs: map[string]*ast.Program{"Товары": prog},
	})

	res, err := svc.Save(context.Background(), SaveRequest{
		Entity: cat, ID: uuid.New(), IsNew: true,
		Fields: map[string]any{"Наименование": "Гвоздь"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.DSLError == "" {
		t.Fatal("ожидалась DSL-ошибка")
	}
	d.Wait()
	if len(sink.bodies) != 0 {
		t.Fatalf("веб-хук ушёл при отменённом сохранении: %+v", sink.bodies)
	}
}

func TestSave_DefersWebhookUntilExplicitTransactionCommit(t *testing.T) {
	sink := &hookSink{}
	srv := httptest.NewServer(sink.handler())
	defer srv.Close()

	cat := &metadata.Entity{
		Name: "Товары", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	svc, d, db := newWebhookSvc(t, []*metadata.Entity{cat}, []webhook.Config{{
		Name: "tx-hook", On: "catalog.save", URL: srv.URL, Body: `{"id":"{{id}}"}`,
	}})

	tx, txCtx, err := db.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rolledBackID := uuid.New()
	if _, err := svc.Save(txCtx, SaveRequest{
		Entity: cat, ID: rolledBackID, IsNew: true,
		Fields: map[string]any{"Наименование": "Откат"},
	}); err != nil {
		t.Fatal(err)
	}
	d.Wait()
	if got := sink.count(); got != 0 {
		t.Fatalf("webhook dispatched before rollback: %d", got)
	}
	if err := tx.Rollback(txCtx); err != nil {
		t.Fatal(err)
	}
	d.Wait()
	if got := sink.count(); got != 0 {
		t.Fatalf("webhook dispatched for rolled-back save: %d", got)
	}

	tx, txCtx, err = db.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	committedID := uuid.New()
	if _, err := svc.Save(txCtx, SaveRequest{
		Entity: cat, ID: committedID, IsNew: true,
		Fields: map[string]any{"Наименование": "Коммит"},
	}); err != nil {
		t.Fatal(err)
	}
	d.Wait()
	if got := sink.count(); got != 0 {
		t.Fatalf("webhook dispatched before commit: %d", got)
	}
	if err := tx.Commit(txCtx); err != nil {
		t.Fatal(err)
	}
	d.Wait()
	if got := sink.count(); got != 1 {
		t.Fatalf("webhook count after commit = %d, want 1", got)
	}
}
