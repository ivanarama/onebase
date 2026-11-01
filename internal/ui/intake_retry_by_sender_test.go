package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Обработчик падает, пока в справочнике Флаг нет записи, — временный сбой
// (например, недоступна база 1С). Надёжный канал Callista повторяет доставку
// сам (outbox, до 12 попыток) и 202 считает окончательным карантином.
const intakeFlakyHandlerSrc = `
Функция Обработать(Конверт) Экспорт
    З = Новый Запрос;
    З.Текст = "ВЫБРАТЬ Ф.Наименование ИЗ Справочник.Флаг КАК Ф";
    Если З.Выполнить().Выгрузить().Количество() = 0 Тогда
        ВызватьИсключение "временный сбой";
    КонецЕсли;
    Возврат "ref-" + Строка(Конверт.Получить("event_id"));
КонецФункции
`

func newFlakyIntakeServer(t *testing.T, retryBySender bool) (*Server, *metadata.Entity) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "intake-retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return newFlakyIntakeServerWithDB(t, db, retryBySender)
}

func newFlakyIntakeServerWithDB(t *testing.T, db *storage.DB, retryBySender bool) (*Server, *metadata.Entity) {
	t.Helper()
	ctx := context.Background()
	flag := &metadata.Entity{Name: "Флаг", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
	if err := db.Migrate(ctx, []*metadata.Entity{flag}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureIntakeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	prog, err := parser.New(lexer.New(intakeFlakyHandlerSrc, "events.module.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{flag}})
	registry.LoadModules(map[string]*ast.Program{"SiteLead": prog})
	in := &metadata.Intake{
		Name: "SiteLead", Transport: "http", Endpoint: "/hs/site/lead", Handler: "SiteLead", Auth: "none",
		Idempotency: metadata.IntakeIdempotency{Key: "event_id", Scope: []string{"source"}},
		DLQ:         metadata.IntakeDLQ{On: []string{metadata.DLQHandlerError, metadata.DLQSchemaMismatch}, RetryBySender: retryBySender},
	}
	in.Normalize()
	registry.LoadIntakes([]*metadata.Intake{in})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	if err := db.SaveNetworkEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	srv := &Server{store: db, reg: registry, interp: interp, lockMgr: runtime.NewLockManager(),
		messages: NewMessageStore(), maxFileSizeBytes: 1 << 20}
	srv.entitySvc = srv.newEntityService(nil)
	return srv, flag
}

func TestIntakeHTTP_RetryBySender_HandlerErrorIs503AndRetryReplays(t *testing.T) {
	s, flag := newFlakyIntakeServer(t, true)
	body := `{"event_id":"E1","source":"callista","payload":{"kind":"ended"}}`

	for i := 0; i < 2; i++ {
		w, got := postIntake(t, s, body)
		if w.Code != http.StatusServiceUnavailable || got["reason"] != metadata.DLQHandlerError {
			t.Fatalf("попытка %d: code=%d body=%v, ждали 503 handler_error", i+1, w.Code, got)
		}
	}
	open, err := s.store.ListIntakeDLQ(context.Background(), "SiteLead", true, 10)
	if err != nil || len(open) != 1 || open[0].Attempts < 1 {
		t.Fatalf("копия в карантине: %+v %v — ждали одну открытую запись с подсчётом попыток", open, err)
	}

	// Сбой прошёл — следующий повтор отправителя переигрывает карантин.
	if err := s.store.Upsert(context.Background(), flag.Name, uuid.New(), map[string]any{"Наименование": "ok"}, flag); err != nil {
		t.Fatal(err)
	}
	w, got := postIntake(t, s, body)
	if w.Code != http.StatusOK || got["status"] != "Принято" || got["ref"] != "ref-E1" {
		open, _ := s.store.ListIntakeDLQ(context.Background(), "SiteLead", true, 10)
		t.Fatalf("повтор после сбоя: code=%d body=%v dlq=%+v", w.Code, got, open)
	}
	w, got = postIntake(t, s, body)
	if w.Code != http.StatusOK || got["status"] != "Дубль" || got["ref"] != "ref-E1" {
		t.Fatalf("дубль после принятия: code=%d body=%v", w.Code, got)
	}
	if open, _ := s.store.ListIntakeDLQ(context.Background(), "SiteLead", true, 10); len(open) != 0 {
		t.Fatalf("запись карантина осталась открытой: %+v", open)
	}

	// Несовпадение тела повтором не исправить — ответ прежний, 202.
	w, got = postIntake(t, s, `{"event_id":"E1","source":"callista","payload":{"kind":"другое"}}`)
	if w.Code != http.StatusAccepted || got["reason"] != metadata.DLQSchemaMismatch {
		t.Fatalf("schema_mismatch: code=%d body=%v", w.Code, got)
	}
}

// Без retry_by_sender поведение прежнее: сбой обработчика — 202 карантин, повтор
// тем же ключом — already_quarantined.
func TestIntakeHTTP_WithoutRetryBySender_HandlerErrorStays202(t *testing.T) {
	s, _ := newFlakyIntakeServer(t, false)
	body := `{"event_id":"E2","source":"callista","payload":{"kind":"ended"}}`
	w, got := postIntake(t, s, body)
	if w.Code != http.StatusAccepted || got["reason"] != metadata.DLQHandlerError {
		t.Fatalf("code=%d body=%v", w.Code, got)
	}
	w, got = postIntake(t, s, body)
	if w.Code != http.StatusAccepted || got["reason"] != "already_quarantined" {
		t.Fatalf("повтор: code=%d body=%v", w.Code, got)
	}
}

// Pause the real DSL handler after Ingest has committed the received key. The
// second HTTP delivery must wait for its outcome rather than start a replay.
type intakeHandlerPause struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (*intakeHandlerPause) HookCheckBreakpoint(string, int, func(string) (bool, error)) bool {
	return false
}
func (*intakeHandlerPause) HookShouldStep(string, int) bool { return false }
func (*intakeHandlerPause) HookOnPause(string, int, map[string]any, func(string) (any, error), string) {
}
func (*intakeHandlerPause) HookPopFrame() {}
func (p *intakeHandlerPause) HookPushFrame(string, int) {
	p.once.Do(func() {
		close(p.started)
		<-p.release
	})
}

func TestIntakeHTTP_OverlappingHandlerFailure(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, retryBySender := range []bool{true, false} {
			name := "legacy202"
			wantCode := http.StatusAccepted
			if retryBySender {
				name = "retry503"
				wantCode = http.StatusServiceUnavailable
			}
			t.Run(name, func(t *testing.T) {
				s, _ := newFlakyIntakeServerWithDB(t, db, retryBySender)
				pause := &intakeHandlerPause{started: make(chan struct{}), release: make(chan struct{})}
				s.interp.DebugSource = func() interpreter.DebugHook { return pause }
				var releaseOnce sync.Once
				release := func() { releaseOnce.Do(func() { close(pause.release) }) }
				defer release()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				body := `{"event_id":"` + name + `","source":"callista","payload":{"kind":"ended"}}`
				post := func(done chan<- *httptest.ResponseRecorder) {
					w := httptest.NewRecorder()
					r := httptest.NewRequest(http.MethodPost, "/hs/site/lead", strings.NewReader(body)).WithContext(ctx)
					r.Header.Set("Content-Type", "application/json")
					s.serviceDispatch(w, r)
					done <- w
				}
				first, second := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
				go post(first)
				select {
				case <-pause.started:
				case <-ctx.Done():
					t.Fatal("first HTTP delivery did not enter its DSL handler")
				}
				go post(second)
				select {
				case w := <-second:
					t.Fatalf("overlapping delivery returned before the handler: %d %s", w.Code, w.Body.String())
				case <-time.After(100 * time.Millisecond):
				}
				release()
				for _, done := range []<-chan *httptest.ResponseRecorder{first, second} {
					select {
					case w := <-done:
						var got map[string]any
						if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
							t.Fatal(err)
						}
						if w.Code != wantCode || got["status"] != "Карантин" {
							t.Fatalf("code=%d body=%v, want %d quarantine", w.Code, got, wantCode)
						}
						if retryBySender && got["reason"] != metadata.DLQHandlerError {
							t.Fatalf("retryable handler failure lost: %v", got)
						}
					case <-ctx.Done():
						t.Fatal("overlapping HTTP deliveries did not finish")
					}
				}
				entries, err := db.ListIntakeDLQ(ctx, "SiteLead", true, 10)
				if err != nil {
					t.Fatal(err)
				}
				var found bool
				for _, entry := range entries {
					if entry.Key == name {
						found = true
						// SQLite serializes the transactions on one connection;
						// PostgreSQL must exercise received -> quarantined waiting.
						if db.Dialect().Name() == "postgres" && entry.Attempts != 0 {
							t.Fatalf("overlap missed: delivery replayed DLQ: %+v", entry)
						}
					}
				}
				if !found {
					t.Fatal("failed delivery missing from DLQ")
				}
			})
		}
	})
}
