package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Обработчик шлюза для теста: возвращает ссылку из event_id. Сущность не нужна —
// проверяем транспорт, адаптер и машину состояний приёмки, а не запись документа
// (это покрыто юнит-тестами internal/intake).
const intakeHandlerSrc = `
Функция Обработать(Конверт) Экспорт
    Возврат "ref-" + Строка(Конверт.Получить("event_id"));
КонецФункции
`

func newIntakeTestServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureIntakeSchema(ctx); err != nil {
		t.Fatal(err)
	}

	prog, err := parser.New(lexer.New(intakeHandlerSrc, "sitelead.module.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.LoadModules(map[string]*ast.Program{"SiteLead": prog})

	in := &metadata.Intake{
		Name: "SiteLead", Transport: "http", Endpoint: "/hs/site/lead", Handler: "SiteLead", Auth: "none",
		Idempotency: metadata.IntakeIdempotency{Key: "event_id", Scope: []string{"source"}},
	}
	in.Normalize()
	registry.LoadIntakes([]*metadata.Intake{in})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	// Предохранитель сети (план 62) включаем — проверяем приёмку, не блокировку.
	if err := db.SaveNetworkEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}

	srv := &Server{
		store:            db,
		reg:              registry,
		interp:           interp,
		lockMgr:          runtime.NewLockManager(),
		messages:         NewMessageStore(),
		maxFileSizeBytes: 1 << 20,
	}
	srv.entitySvc = srv.newEntityService(nil)
	return srv
}

func postIntake(t *testing.T, s *Server, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/hs/site/lead", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	s.serviceDispatch(w, r) // реальный путь /hs/*: сперва шлюзы, затем сервисы
	var got map[string]any
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &got)
	}
	return w, got
}

func TestIntakeHTTP_HappyAndDuplicate(t *testing.T) {
	s := newIntakeTestServer(t)
	body := `{"event_id":"A1","source":"site","payload":{"x":1}}`

	w, got := postIntake(t, s, body)
	if w.Code != http.StatusOK || got["status"] != "Принято" || got["ref"] != "ref-A1" {
		t.Fatalf("happy: code=%d body=%v", w.Code, got)
	}

	// Тот же ключ и тело → Дубль, тот же ref, 200.
	w, got = postIntake(t, s, body)
	if w.Code != http.StatusOK || got["status"] != "Дубль" || got["ref"] != "ref-A1" {
		t.Fatalf("duplicate: code=%d body=%v", w.Code, got)
	}
}

func TestIntakeHTTP_Mismatch(t *testing.T) {
	s := newIntakeTestServer(t)
	postIntake(t, s, `{"event_id":"A1","source":"site","payload":{"x":1}}`)

	// Тот же event_id, другое тело → карантин (202), schema_mismatch.
	w, got := postIntake(t, s, `{"event_id":"A1","source":"site","payload":{"x":999}}`)
	if w.Code != http.StatusAccepted || got["status"] != "Карантин" || got["reason"] != "schema_mismatch" {
		t.Fatalf("mismatch: code=%d body=%v", w.Code, got)
	}
	if got["dlq_id"] == nil || got["dlq_id"] == "" {
		t.Fatalf("mismatch: не заполнен dlq_id: %v", got)
	}
}

func TestIntakeHTTP_RejectNoKey(t *testing.T) {
	s := newIntakeTestServer(t)
	w, got := postIntake(t, s, `{"source":"site","payload":{"x":1}}`)
	if w.Code != http.StatusUnprocessableEntity || got["status"] != "Отклонено" {
		t.Fatalf("reject: code=%d body=%v", w.Code, got)
	}
}

func TestIntakeHTTP_MethodNotAllowed(t *testing.T) {
	s := newIntakeTestServer(t)
	w := httptest.NewRecorder()
	s.serviceDispatch(w, httptest.NewRequest("GET", "/hs/site/lead", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET на приёмник: code=%d, ожидался 405", w.Code)
	}
}

func TestIntakeHTTP_AuthToken(t *testing.T) {
	s := newIntakeTestServer(t)
	// Переключаем шлюз на token-аутентификацию.
	in := s.reg.GetIntake("SiteLead")
	in.Auth = metadata.IntakeAuthToken
	in.Secret = "s3cret"

	body := `{"event_id":"T1","source":"site","payload":{"x":1}}`

	// Без токена → 401.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/hs/site/lead", strings.NewReader(body))
	s.serviceDispatch(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("без токена: code=%d, ожидался 401", w.Code)
	}

	// С верным токеном → 200 Принято.
	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/hs/site/lead", strings.NewReader(body))
	r.Header.Set("X-Webhook-Token", "s3cret")
	s.serviceDispatch(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("с токеном: code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestMapHandlerResult_ReferenceFieldIsCaseInsensitive(t *testing.T) {
	got := mapHandlerResult(map[string]any{"Ссылка": "ref-42", "ok": true})
	if got.Ref != "ref-42" {
		t.Fatalf("Ref=%q, ожидалось ref-42", got.Ref)
	}
	if got.BusinessResult["ok"] != true {
		t.Fatalf("business result потерян: %+v", got.BusinessResult)
	}
}
