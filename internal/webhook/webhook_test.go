package webhook

// Тесты диспетчера исходящих веб-хуков (план 29): фильтры, шаблоны тела,
// retry с экспоненциальной задержкой, журналирование.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recorder — тестовый приёмник веб-хуков.
type recorder struct {
	mu     sync.Mutex
	bodies []string
	heads  []http.Header
	fails  int32 // сколько первых запросов вернуть с 500
}

func (rec *recorder) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		rec.mu.Lock()
		rec.bodies = append(rec.bodies, sb.String())
		rec.heads = append(rec.heads, r.Header.Clone())
		rec.mu.Unlock()
		if atomic.AddInt32(&rec.fails, -1) >= 0 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func (rec *recorder) count() int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return len(rec.bodies)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("условие не выполнилось за 5 секунд")
}

func TestDispatcher_FiresOnMatchingEvent(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	d := New([]Config{{
		Name:    "tg",
		On:      "document.post",
		Filter:  map[string]string{"entity": "Реализация"},
		URL:     srv.URL,
		Method:  "POST",
		Headers: map[string]string{"Content-Type": "application/json", "X-Token": "abc"},
		Body:    `{"text": "Документ {{id}} ({{entity}}) на сумму {{Сумма}} проведён пользователем {{user}}"}`,
	}}, nil)

	d.Dispatch(Event{
		Name:   "document.post",
		Entity: "Реализация",
		ID:     "11111111-2222-3333-4444-555555555555",
		User:   "ivan",
		Record: map[string]any{"Сумма": 1500},
	})
	d.Wait()

	if rec.count() != 1 {
		t.Fatalf("ожидался 1 запрос, получено %d", rec.count())
	}
	body := rec.bodies[0]
	var parsed map[string]string
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("тело не JSON: %v (%s)", err, body)
	}
	want := "Документ 11111111-2222-3333-4444-555555555555 (Реализация) на сумму 1500 проведён пользователем ivan"
	if parsed["text"] != want {
		t.Fatalf("тело: %q, ожидалось %q", parsed["text"], want)
	}
	if rec.heads[0].Get("X-Token") != "abc" {
		t.Fatal("кастомный заголовок не передан")
	}
}

func TestDispatcher_FilterAndEventMismatch(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	d := New([]Config{
		{Name: "a", On: "document.post", Filter: map[string]string{"entity": "Реализация"}, URL: srv.URL, Body: "x"},
		{Name: "b", On: "catalog.save", URL: srv.URL, Body: "y"},
	}, nil)

	// не то событие
	d.Dispatch(Event{Name: "document.save", Entity: "Реализация"})
	// то событие, но не та сущность
	d.Dispatch(Event{Name: "document.post", Entity: "Заказ"})
	d.Wait()

	if rec.count() != 0 {
		t.Fatalf("ожидалось 0 запросов, получено %d", rec.count())
	}

	// catalog.save без фильтра — срабатывает на любую сущность
	d.Dispatch(Event{Name: "catalog.save", Entity: "Контрагенты"})
	d.Wait()
	if rec.count() != 1 {
		t.Fatalf("хук без фильтра должен сработать, запросов: %d", rec.count())
	}
}

func TestDispatcher_RetriesOn5xx(t *testing.T) {
	rec := &recorder{fails: 2} // первые два запроса → 500
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	logged := make(chan LogEntry, 1)
	d := New([]Config{{
		Name: "r", On: "document.save", URL: srv.URL, Body: "x", Retry: 2,
	}}, func(e LogEntry) { logged <- e })
	d.retryBase = time.Millisecond // ускоряем экспоненту в тесте

	d.Dispatch(Event{Name: "document.save", Entity: "Заказ", ID: "id1"})
	d.Wait()

	if rec.count() != 3 {
		t.Fatalf("ожидалось 3 попытки (1 + 2 retry), получено %d", rec.count())
	}
	e := <-logged
	if e.StatusCode != 200 || e.Webhook != "r" || e.Event != "document.save" {
		t.Fatalf("лог: %+v", e)
	}
	m := d.Metrics()
	if m.Inflight != 0 || m.Dispatched != 1 || m.Retries != 2 || m.Failed != 0 {
		t.Fatalf("metrics = %+v, want inflight=0 dispatched=1 retries=2 failed=0", m)
	}
}

func TestDispatcher_LogsFailure(t *testing.T) {
	logged := make(chan LogEntry, 1)
	d := New([]Config{{
		Name: "dead", On: "document.save", URL: "http://127.0.0.1:1/unreachable", Body: "x",
	}}, func(e LogEntry) { logged <- e })
	d.retryBase = time.Millisecond

	d.Dispatch(Event{Name: "document.save", Entity: "Заказ"})
	d.Wait()

	e := <-logged
	if e.Error == "" {
		t.Fatalf("ожидалась ошибка в логе, получено %+v", e)
	}
	if m := d.Metrics(); m.Failed != 1 {
		t.Fatalf("failed metric = %d, want 1", m.Failed)
	}
}

// Строковые значения экранируются для безопасной вставки внутрь JSON-строк.
func TestDispatcher_EscapesJSONInStrings(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	d := New([]Config{{
		Name: "j", On: "catalog.save", URL: srv.URL,
		Body: `{"name": "{{Наименование}}"}`,
	}}, nil)

	d.Dispatch(Event{Name: "catalog.save", Entity: "Товары",
		Record: map[string]any{"Наименование": `Труба "стальная"` + "\nдвухдюймовая"}})
	d.Wait()

	if rec.count() != 1 {
		t.Fatalf("запросов: %d", rec.count())
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(rec.bodies[0]), &parsed); err != nil {
		t.Fatalf("кавычки/переводы строк сломали JSON: %v (%s)", err, rec.bodies[0])
	}
	if !strings.Contains(parsed["name"], `"стальная"`) {
		t.Fatalf("значение исказилось: %q", parsed["name"])
	}
}

// Предохранитель сети (план 62): при guard()==false хук не отправляется,
// но в журнал попадает запись со статусом «заблокировано».
func TestDispatcher_BlockedByNetGuard(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	logged := make(chan LogEntry, 1)
	d := New([]Config{{Name: "g", On: "document.post", URL: srv.URL, Body: "x"}},
		func(e LogEntry) { logged <- e })
	d.SetGuard(func() bool { return false }) // сеть заблокирована

	d.Dispatch(Event{Name: "document.post", Entity: "Реализация", ID: "id1"})
	d.Wait()

	if rec.count() != 0 {
		t.Fatalf("при заблокированной сети хук не должен уходить, запросов: %d", rec.count())
	}
	e := <-logged
	if e.Error == "" || !strings.Contains(e.Error, "предохранител") {
		t.Fatalf("ожидалась запись о блокировке, получено: %+v", e)
	}
}

// При guard()==true хук уходит как обычно.
func TestDispatcher_AllowedByNetGuard(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	d := New([]Config{{Name: "g", On: "document.post", URL: srv.URL, Body: "x"}}, nil)
	d.SetGuard(func() bool { return true })

	d.Dispatch(Event{Name: "document.post", Entity: "Реализация", ID: "id1"})
	d.Wait()

	if rec.count() != 1 {
		t.Fatalf("при разрешённой сети ожидался 1 запрос, получено %d", rec.count())
	}
}

// Отрицательный retry (мисконфигурация app.yaml) не должен отключать веб-хук
// целиком: кламп только сверху оставлял attempts = retry+1 = 0, и цикл отправки
// не выполнялся ни разу. Должна выполниться хотя бы одна попытка.
func TestDispatcher_NegativeRetryStillFiresOnce(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	d := New([]Config{{Name: "n", On: "document.post", URL: srv.URL, Body: "x", Retry: -1}}, nil)
	d.Dispatch(Event{Name: "document.post", Entity: "Реализация", ID: "id1"})
	d.Wait()

	if rec.count() != 1 {
		t.Fatalf("при retry=-1 ожидалась 1 отправка, получено %d", rec.count())
	}
}

func TestDispatcher_BoundedQueueDropsAndLogsOverflow(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var mu sync.Mutex
	var logs []LogEntry
	d := NewWithOptions([]Config{{
		Name: "bounded", On: "document.save", URL: srv.URL,
	}}, func(entry LogEntry) {
		mu.Lock()
		logs = append(logs, entry)
		mu.Unlock()
	}, Options{Workers: 1, QueueSize: 1})

	d.Dispatch(Event{Name: "document.save", ID: "1"})
	<-started                                         // worker is occupied by the first delivery
	d.Dispatch(Event{Name: "document.save", ID: "2"}) // fills the queue
	d.Dispatch(Event{Name: "document.save", ID: "3"}) // must be dropped
	close(release)
	d.Wait()

	if got := requests.Load(); got != 2 {
		t.Fatalf("HTTP requests = %d, want 2", got)
	}
	metrics := d.Metrics()
	if metrics.Inflight != 0 || metrics.Dispatched != 3 || metrics.Dropped != 1 || metrics.Failed != 1 {
		t.Fatalf("metrics = %+v, want dispatched=3 dropped=1 failed=1 inflight=0", metrics)
	}
	mu.Lock()
	defer mu.Unlock()
	var overflow bool
	for _, entry := range logs {
		if strings.Contains(entry.Error, "переполнена") && entry.Attempts == 0 {
			overflow = true
		}
	}
	if !overflow {
		t.Fatalf("overflow was not journalled: %+v", logs)
	}
	closeDispatcher(t, d)
}

func TestDispatcher_RedactsSecretsFromURLAndErrors(t *testing.T) {
	t.Run("successful request", func(t *testing.T) {
		logged := make(chan LogEntry, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()
		d := New([]Config{{
			Name: "secret", On: "document.post",
			URL: srv.URL + "/botSUPERSECRET/send?token=QUERYSECRET",
		}}, func(entry LogEntry) { logged <- entry })

		d.Dispatch(Event{Name: "document.post"})
		d.Wait()
		entry := <-logged
		if strings.Contains(entry.URL, "SUPERSECRET") || strings.Contains(entry.URL, "QUERYSECRET") {
			t.Fatalf("secret leaked into URL log: %q", entry.URL)
		}
		if want := srv.URL + "/<redacted>?<redacted>"; entry.URL != want {
			t.Fatalf("redacted URL = %q, want %q", entry.URL, want)
		}
		closeDispatcher(t, d)
	})

	t.Run("network error", func(t *testing.T) {
		logged := make(chan LogEntry, 1)
		d := New([]Config{{
			Name: "secret", On: "document.post",
			URL: "http://127.0.0.1:1/botSUPERSECRET/send?token=QUERYSECRET",
		}}, func(entry LogEntry) { logged <- entry })

		d.Dispatch(Event{Name: "document.post"})
		d.Wait()
		entry := <-logged
		combined := entry.URL + " " + entry.Error
		if strings.Contains(combined, "SUPERSECRET") || strings.Contains(combined, "QUERYSECRET") {
			t.Fatalf("secret leaked into failure log: %+v", entry)
		}
		if entry.Error == "" {
			t.Fatalf("network failure was not logged: %+v", entry)
		}
		closeDispatcher(t, d)
	})
}

func TestRedactURL_RemovesUserInfoPathQueryAndFragment(t *testing.T) {
	raw := "https://user:SUPERSECRET@example.com/botPATHSECRET/send?token=QUERYSECRET#FRAGMENTSECRET"
	got := RedactURL(raw)
	if got != "https://example.com/<redacted>?<redacted>" {
		t.Fatalf("RedactURL = %q", got)
	}
	for _, secret := range []string{"SUPERSECRET", "PATHSECRET", "QUERYSECRET", "FRAGMENTSECRET", "user"} {
		if strings.Contains(got, secret) {
			t.Fatalf("RedactURL leaked %q in %q", secret, got)
		}
	}
	if got := RedactURL("not a valid endpoint SUPERSECRET"); got != "<redacted>" {
		t.Fatalf("invalid URL redaction = %q", got)
	}
}

func TestDispatcher_CloseDrainsQueuedDeliveries(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	d := NewWithOptions([]Config{{Name: "drain", On: "catalog.save", URL: srv.URL}},
		nil, Options{Workers: 1, QueueSize: 2})
	d.Dispatch(Event{Name: "catalog.save", ID: "1"})
	<-started
	d.Dispatch(Event{Name: "catalog.save", ID: "2"})

	closed := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		closed <- d.Close(ctx)
	}()
	select {
	case err := <-closed:
		t.Fatalf("Close returned before active delivery completed: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-closed; err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("Close delivered %d requests, want 2", got)
	}
}

func TestDispatcher_CloseDeadlineCancelsRetryBackoff(t *testing.T) {
	firstAttempt := make(chan struct{}, 1)
	logged := make(chan LogEntry, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case firstAttempt <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	d := New([]Config{{
		Name: "cancel", On: "document.save", URL: srv.URL, Retry: 10,
	}}, func(entry LogEntry) { logged <- entry })
	d.retryBase = time.Hour
	d.Dispatch(Event{Name: "document.save"})
	<-firstAttempt

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := d.Close(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v, want context deadline", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Close did not cancel retry backoff promptly: %v", elapsed)
	}
	entry := <-logged
	if !strings.Contains(entry.Error, "отменена") {
		t.Fatalf("cancelled delivery log = %+v", entry)
	}
}

func closeDispatcher(t *testing.T, d *Dispatcher) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := d.Close(ctx); err != nil {
		t.Fatalf("Close dispatcher: %v", err)
	}
}
