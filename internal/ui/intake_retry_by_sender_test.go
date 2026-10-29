package ui

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
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
