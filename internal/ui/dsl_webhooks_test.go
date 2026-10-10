package ui

// Регрессия (план 29): путь Документы.X из DSL не проходит через
// entityservice.Save, где живёт диспетчеризация веб-хуков, поэтому документы,
// записанные обработкой / HTTP-сервисом / регламентным заданием, событий не
// порождали — интеграции их не видели. Справочников это не касалось: catWriter
// сохраняется через entityservice.Save.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/webhook"
)

// errRollbackDSLWebhook — маркер намеренного отката транзакции в тесте.
var errRollbackDSLWebhook = errors.New("намеренный откат")

type dslHookSink struct {
	mu     sync.Mutex
	events []string
}

func (h *dslHookSink) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		h.mu.Lock()
		if s, ok := body["event"].(string); ok {
			h.events = append(h.events, s)
		}
		h.mu.Unlock()
		w.WriteHeader(200)
	}
}

func (h *dslHookSink) sorted() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := append([]string(nil), h.events...)
	sort.Strings(out)
	return out
}

// dslWebhookServer поднимает Server с подключённым диспетчером хуков на все
// четыре события документа.
func dslWebhookServer(t *testing.T, url string, doc *metadata.Entity, progs map[string]*ast.Program) (*Server, *webhook.Dispatcher, *storage.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "wh.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc}, Programs: progs})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	var hooks []webhook.Config
	for _, ev := range []string{"document.save", "document.post", "document.unpost", "document.delete"} {
		hooks = append(hooks, webhook.Config{
			Name: ev, On: ev, URL: url,
			Body: `{"event":"` + ev + `","id":"{{id}}","number":"{{Номер}}"}`,
		})
	}
	d := webhook.New(hooks, nil)
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = d.Close(c)
	})

	s := &Server{
		store: db, reg: registry, interp: interp,
		lockMgr: runtime.NewLockManager(), messages: NewMessageStore(),
		cfg: Config{Webhooks: d},
	}
	s.entitySvc = s.newEntityService(d)
	return s, d, db, ctx
}

func dslWebhookDoc() *metadata.Entity {
	return &metadata.Entity{
		Name:    "ПоступлениеТоваров",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields:  []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
}

// Записать/Провести/ОтменитьПроведение/Удалить из DSL публикуют события.
func TestDSLDocuments_DispatchWebhooks(t *testing.T) {
	sink := &dslHookSink{}
	srv := httptest.NewServer(sink.handler())
	defer srv.Close()

	s, d, _, ctx := dslWebhookServer(t, srv.URL, dslWebhookDoc(), nil)

	root := newDocsRoot(s, interpreter.NewTxState(ctx))
	dp := root.Get("ПоступлениеТоваров").(*docProxy)

	w := dp.CallMethod("создать", nil).(*docWriter)
	w.Set("Номер", "ПОС-001")
	w.CallMethod("записать", nil)
	d.Wait()
	if got := sink.sorted(); len(got) != 1 || got[0] != "document.save" {
		t.Fatalf("после Записать ожидался document.save, получено %v", got)
	}

	// Провести() объединяет неявную запись и проведение в одно финальное post.
	w.CallMethod("провести", nil)
	d.Wait()
	if got := sink.sorted(); len(got) != 2 || got[0] != "document.post" || got[1] != "document.save" {
		t.Fatalf("после Провести ожидались два события (save, post), получено %v", got)
	}

	ref := dp.CallMethod("найтипономеру", []any{"ПОС-001"}).(*interpreter.Ref)
	dp.CallMethod("отменитьпроведение", []any{ref})
	d.Wait()
	dp.CallMethod("удалить", []any{ref})
	d.Wait()

	got := sink.sorted()
	want := []string{"document.delete", "document.post", "document.save", "document.unpost"}
	if len(got) != len(want) {
		t.Fatalf("событий %d, ждали %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("события: получили %v, ждали %v", got, want)
		}
	}
}

// В явной DSL-транзакции событие уходит только после коммита: откат не должен
// сообщать наружу о данных, которых в базе нет.
func TestDSLDocuments_WebhookDeferredUntilCommit(t *testing.T) {
	sink := &dslHookSink{}
	srv := httptest.NewServer(sink.handler())
	defer srv.Close()

	s, d, db, ctx := dslWebhookServer(t, srv.URL, dslWebhookDoc(), nil)

	// Откат: событие не публикуется.
	err := db.WithTxScope(ctx, func(txCtx context.Context) error {
		root := newDocsRoot(s, interpreter.NewTxState(txCtx))
		dp := root.Get("ПоступлениеТоваров").(*docProxy)
		w := dp.CallMethod("создать", nil).(*docWriter)
		w.Set("Номер", "ОТКАТ-1")
		w.CallMethod("записать", nil)
		return errRollbackDSLWebhook
	})
	if err == nil {
		t.Fatal("ожидалась ошибка отката транзакции")
	}
	d.Wait()
	if got := sink.sorted(); len(got) != 0 {
		t.Fatalf("после отката событий быть не должно, получено %v", got)
	}

	// Коммит: событие уходит.
	if err := db.WithTxScope(ctx, func(txCtx context.Context) error {
		root := newDocsRoot(s, interpreter.NewTxState(txCtx))
		dp := root.Get("ПоступлениеТоваров").(*docProxy)
		w := dp.CallMethod("создать", nil).(*docWriter)
		w.Set("Номер", "КОММИТ-1")
		w.CallMethod("записать", nil)
		return nil
	}); err != nil {
		t.Fatalf("запись в транзакции: %v", err)
	}
	d.Wait()
	if got := sink.sorted(); len(got) != 1 || got[0] != "document.save" {
		t.Fatalf("после коммита ожидался document.save, получено %v", got)
	}
}
