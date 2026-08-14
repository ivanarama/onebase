package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/metrics"
)

// Обработчик события управляемой формы обязан отрубаться по дедлайну и
// подчиняться пределу конкурентности (#865).
//
// handleProcessorFormEvent из того же PR #735 и то и другое получил, а
// handleManagedFormEvent — самый обычный путь, кнопка на форме объекта —
// исполнял DSL напрямую s.interp.Run: без предела времени вовсе и без слота
// операций. Один обработчик с Приостановить(300) занимал соединение и держал
// пользователя пять минут.

func TestСобытиеФормы_ОтрубаетсяПоДедлайну(t *testing.T) {
	s, ent := setupManagedEventsServer(t, `
Процедура Долго()
	Приостановить(300);
КонецПроцедуры
`, map[metadata.FormEventType]string{}, []*metadata.FormElement{{
		Kind:     metadata.FormElementButton,
		Name:     "Кнопка",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Долго"},
	}})
	// Предел берётся из общего лимита запроса.
	s.cfg.Limits.RequestTimeoutSec = 1

	body := url.Values{"_element": {"Кнопка"}, "_event": {string(metadata.FormEventOnClick)}, "_kind": {"object"}}
	start := time.Now()
	rec := executeFormEventRaw(t, s, ent, body)
	elapsed := time.Since(start)

	if elapsed > 30*time.Second {
		t.Fatalf("обработчик выполнялся %v — дедлайна нет", elapsed)
	}
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if resp.Error == "" {
		t.Fatalf("Приостановить(300) прошло успешно за %v — предела нет", elapsed)
	}
	if !strings.Contains(resp.Error, "врем") {
		t.Errorf("ошибка не объясняет причину: %q", resp.Error)
	}
}

func TestFormEventDeadline_CancelsBlockingHTTPAndRecordsTimeout(t *testing.T) {
	requestCanceled := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			close(requestCanceled)
		case <-time.After(4 * time.Second):
			_, _ = fmt.Fprint(w, "too late")
		}
	}))
	defer srv.Close()

	s, ent := setupManagedEventsServer(t, fmt.Sprintf(`
Procedure Slow()
  HTTPGet("%s");
EndProcedure
`, srv.URL), map[metadata.FormEventType]string{}, []*metadata.FormElement{{
		Kind:     metadata.FormElementButton,
		Name:     "SlowButton",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Slow"},
	}})
	if err := s.store.SaveNetworkEnabled(context.Background(), true); err != nil {
		t.Fatalf("enable network: %v", err)
	}
	s.cfg.Limits.RequestTimeoutSec = 1
	s.cfg.Metrics = metrics.New()

	body := url.Values{"_element": {"SlowButton"}, "_event": {string(metadata.FormEventOnClick)}, "_kind": {"object"}}
	started := time.Now()
	rec := executeFormEventRaw(t, s, ent, body)
	if elapsed := time.Since(started); elapsed > 2500*time.Millisecond {
		t.Fatalf("blocking HTTP outlived form event deadline: %v", elapsed)
	}
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !strings.Contains(strings.ToLower(resp.Error), "врем") {
		t.Fatalf("expected form event deadline error, got %q", resp.Error)
	}
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("form event deadline did not cancel HTTP request context")
	}

	var out strings.Builder
	if err := s.cfg.Metrics.WritePrometheus(&out); err != nil {
		t.Fatalf("write metrics: %v", err)
	}
	if !strings.Contains(out.String(), `onebase_operation_total{kind="form.event",status="timeout"} 1`) {
		t.Fatalf("form event timeout recorded with wrong status:\n%s", out.String())
	}
	if strings.Contains(out.String(), `onebase_operation_total{kind="form.event",status="ok"}`) {
		t.Fatalf("failed form event was recorded as ok:\n%s", out.String())
	}
}

func TestFormEventErrorRecordsErrorStatus(t *testing.T) {
	s, ent := setupManagedEventsServer(t, `
Procedure Fail()
  Raise "boom";
EndProcedure
`, map[metadata.FormEventType]string{}, []*metadata.FormElement{{
		Kind:     metadata.FormElementButton,
		Name:     "FailButton",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Fail"},
	}})
	s.cfg.Metrics = metrics.New()
	body := url.Values{"_element": {"FailButton"}, "_event": {string(metadata.FormEventOnClick)}, "_kind": {"object"}}
	resp := decodeFormEventResponse(t, executeFormEventRaw(t, s, ent, body).Body.Bytes())
	if !strings.Contains(resp.Error, "boom") {
		t.Fatalf("expected handler error, got %q", resp.Error)
	}
	var out strings.Builder
	if err := s.cfg.Metrics.WritePrometheus(&out); err != nil {
		t.Fatalf("write metrics: %v", err)
	}
	if !strings.Contains(out.String(), `onebase_operation_total{kind="form.event",status="error"} 1`) {
		t.Fatalf("form event error recorded with wrong status:\n%s", out.String())
	}
	if strings.Contains(out.String(), `onebase_operation_total{kind="form.event",status="ok"}`) {
		t.Fatalf("failed form event was recorded as ok:\n%s", out.String())
	}
}

func TestFormEventDeadline_CancelsBlockedPreflightDB(t *testing.T) {
	s, ent := setupManagedEventsServer(t, `
Procedure Fast()
EndProcedure
`, map[metadata.FormEventType]string{}, []*metadata.FormElement{{
		Kind:     metadata.FormElementButton,
		Name:     "FastButton",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Fast"},
	}})
	id := uuid.New()
	if err := s.store.Upsert(context.Background(), ent.Name, id, map[string]any{"Наименование": "held"}, ent); err != nil {
		t.Fatalf("seed entity: %v", err)
	}
	tx, _, err := s.store.BeginTx(context.Background())
	if err != nil {
		t.Fatalf("hold SQLite connection: %v", err)
	}
	rolledBack := false
	defer func() {
		if !rolledBack {
			_ = tx.Rollback(context.Background())
		}
	}()

	s.cfg.Limits.RequestTimeoutSec = 1
	s.cfg.Metrics = metrics.New()
	body := url.Values{
		"_id":      {id.String()},
		"_element": {"FastButton"},
		"_event":   {string(metadata.FormEventOnClick)},
		"_kind":    {"object"},
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- executeFormEventRaw(t, s, ent, body) }()

	var rec *httptest.ResponseRecorder
	select {
	case rec = <-done:
	case <-time.After(2500 * time.Millisecond):
		_ = tx.Rollback(context.Background())
		rolledBack = true
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		t.Fatal("preflight DB query ignored form event deadline")
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatalf("release held SQLite connection: %v", err)
	}
	rolledBack = true
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if resp.Error == "" {
		t.Fatal("blocked preflight DB query unexpectedly succeeded")
	}
	var out strings.Builder
	if err := s.cfg.Metrics.WritePrometheus(&out); err != nil {
		t.Fatalf("write metrics: %v", err)
	}
	if !strings.Contains(out.String(), `onebase_operation_total{kind="form.event",status="timeout"} 1`) {
		t.Fatalf("blocked preflight DB timeout recorded with wrong status:\n%s", out.String())
	}
}

// Предел конкурентности: когда слот занят, событие получает 429, а не встаёт
// в очередь на соединение к БД.
//
// Слот занимается напрямую через лимитер, а не вторым живым запросом: гнать
// два прикладных DSL параллельно ради проверки гейта — значит проверять заодно
// потокобезопасность фикстуры, и под -race тест падал именно на ней, а не на
// предмете проверки.
func TestСобытиеФормы_ПределКонкурентности(t *testing.T) {
	s, ent := setupManagedEventsServer(t, `
Процедура Быстро()
	Сообщить("ok");
КонецПроцедуры
`, map[metadata.FormEventType]string{}, []*metadata.FormElement{{
		Kind:     metadata.FormElementButton,
		Name:     "Кнопка",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Быстро"},
	}})
	s.cfg.Limits.ProcessorConcurrency = 1

	body := url.Values{"_element": {"Кнопка"}, "_event": {string(metadata.FormEventOnClick)}, "_kind": {"object"}}

	// Пока слот свободен — событие проходит.
	if rec := executeFormEventRaw(t, s, ent, body); rec.Code != 200 {
		t.Fatalf("при свободном слоте код %d, ожидался 200", rec.Code)
	}

	s.ops = newOperationLimiter()
	release, ok := s.ops.tryAcquire(opFormEvent, 1)
	if !ok {
		t.Fatal("не удалось занять слот в подготовке теста")
	}
	defer release()

	if rec := executeFormEventRaw(t, s, ent, body); rec.Code != 429 {
		t.Errorf("при занятом слоте код %d, ожидался 429 — предела конкурентности нет", rec.Code)
	}
}
