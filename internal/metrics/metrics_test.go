package metrics

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func TestRegistry_RecordsAndExposes(t *testing.T) {
	reg := New()

	r := chi.NewRouter()
	r.Use(reg.Middleware)
	r.Get("/documents/{entity}/{id}/post", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {})

	// Два запроса на проведение и один на health.
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/documents/Счёт/123/post", nil))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	var sb strings.Builder
	if err := reg.WritePrometheus(&sb); err != nil {
		t.Fatalf("WritePrometheus: %v", err)
	}
	out := sb.String()

	// Метка route — это шаблон chi, а не конкретный id (низкая кардинальность).
	if !strings.Contains(out, `onebase_http_requests_total{method="GET",route="/documents/{entity}/{id}/post",status="201"} 2`) {
		t.Errorf("ожидали счётчик проведения =2 с шаблонной меткой route, получили:\n%s", out)
	}
	if !strings.Contains(out, `route="/health",status="200"} 1`) {
		t.Errorf("ожидали счётчик health=1, получили:\n%s", out)
	}
	// Гистограмма: count по маршруту проведения == 2, есть +Inf корзина.
	if !strings.Contains(out, `onebase_http_request_duration_seconds_count{method="GET",route="/documents/{entity}/{id}/post"} 2`) {
		t.Errorf("ожидали histogram count=2, получили:\n%s", out)
	}
	if !strings.Contains(out, `le="+Inf"`) {
		t.Errorf("ожидали корзину +Inf, получили:\n%s", out)
	}
	// TYPE-строки обязательны для валидного exposition.
	if !strings.Contains(out, "# TYPE onebase_http_requests_total counter") ||
		!strings.Contains(out, "# TYPE onebase_http_request_duration_seconds histogram") {
		t.Errorf("отсутствуют TYPE-заголовки, получили:\n%s", out)
	}
}

func TestRegistry_OperationMetrics(t *testing.T) {
	reg := New()
	reg.OperationStart("report.run")
	reg.OperationFinish("report.run", "ok", 25*time.Millisecond, false)
	reg.OperationLimited("http_service.run", "concurrency")

	var sb strings.Builder
	if err := reg.WritePrometheus(&sb); err != nil {
		t.Fatalf("WritePrometheus: %v", err)
	}
	out := sb.String()

	if !strings.Contains(out, `onebase_operation_total{kind="report.run",status="ok"} 1`) {
		t.Errorf("missing operation counter:\n%s", out)
	}
	if !strings.Contains(out, `onebase_active_operations{kind="report.run"} 0`) {
		t.Errorf("missing active operation gauge:\n%s", out)
	}
	if !strings.Contains(out, `onebase_operation_duration_seconds_count{kind="report.run"} 1`) {
		t.Errorf("missing operation duration histogram:\n%s", out)
	}
	if !strings.Contains(out, `onebase_limited_operation_total{kind="http_service.run",reason="concurrency"} 1`) {
		t.Errorf("missing limited operation counter:\n%s", out)
	}
}

func TestRegistry_FuncMetrics(t *testing.T) {
	reg := New()
	reg.RegisterGaugeFunc("onebase_active_sessions", "Active sessions.", func() float64 { return 3 })
	reg.RegisterCounterFunc("onebase_webhook_retry_total", "Webhook retries.", func() float64 { return 7 })

	var sb strings.Builder
	if err := reg.WritePrometheus(&sb); err != nil {
		t.Fatalf("WritePrometheus: %v", err)
	}
	out := sb.String()

	if !strings.Contains(out, "# TYPE onebase_active_sessions gauge") ||
		!strings.Contains(out, "onebase_active_sessions 3") {
		t.Errorf("missing gauge func metric:\n%s", out)
	}
	if !strings.Contains(out, "# TYPE onebase_webhook_retry_total counter") ||
		!strings.Contains(out, "onebase_webhook_retry_total 7") {
		t.Errorf("missing counter func metric:\n%s", out)
	}
}

// Middleware не должен «съедать» http.Flusher: SSE-эндпоинты (real-time-
// уведомления /ui/events, поток сканера ШК) делают w.(http.Flusher), и без
// проксирования Flush через statusRecorder падали бы с «стриминг не
// поддерживается сервером» на любой базе с ONEBASE_DEBUG_TOKEN (лаунчер/GUI).
func TestRegistry_MiddlewarePreservesFlusher(t *testing.T) {
	reg := New()
	r := chi.NewRouter()
	r.Use(reg.Middleware)
	var sawFlusher bool
	r.Get("/ui/events", func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		sawFlusher = ok
		if ok {
			w.WriteHeader(http.StatusOK)
			f.Flush()
		}
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/events", nil))

	if !sawFlusher {
		t.Fatal("хендлер за metrics-middleware не увидел http.Flusher — SSE-стриминг сломан")
	}
	if !rec.Flushed {
		t.Error("Flush() не проброшен до нижележащего ResponseWriter")
	}
}

// Горячий путь безлоковый и точный под гонкой: много горутин пишут метрики,
// параллельно идёт скрейп; после — ни одного потерянного инкремента и инвариант
// гистограммы «+Inf == count» соблюдён (план 111, P2-2). Запускать с -race.
func TestRegistry_ConcurrentObserveIsRaceFreeAndExact(t *testing.T) {
	reg := New()
	const goroutines = 16
	const perG = 500
	const total = goroutines * perG

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				reg.observe("GET", "/documents/{entity}", 200, time.Millisecond)
				reg.OperationStart("report.run")
				reg.OperationFinish("report.run", "ok", time.Millisecond, false)
				reg.OperationLimited("export.run", "concurrency")
			}
		}()
	}

	// Конкурентный скрейп во время записи — ловим гонки чтения/записи.
	stop := make(chan struct{})
	var reader sync.WaitGroup
	reader.Add(1)
	go func() {
		defer reader.Done()
		for {
			select {
			case <-stop:
				return
			default:
				reg.WritePrometheus(io.Discard)
			}
		}
	}()

	wg.Wait()
	close(stop)
	reader.Wait()

	var sb strings.Builder
	if err := reg.WritePrometheus(&sb); err != nil {
		t.Fatalf("WritePrometheus: %v", err)
	}
	out := sb.String()

	// Точные счётчики — ни один инкремент не потерян под гонкой.
	for _, want := range []string{
		fmt.Sprintf(`onebase_http_requests_total{method="GET",route="/documents/{entity}",status="200"} %d`, total),
		fmt.Sprintf(`onebase_http_request_duration_seconds_count{method="GET",route="/documents/{entity}"} %d`, total),
		fmt.Sprintf(`onebase_http_request_duration_seconds_bucket{method="GET",route="/documents/{entity}",le="+Inf"} %d`, total),
		fmt.Sprintf(`onebase_operation_total{kind="report.run",status="ok"} %d`, total),
		fmt.Sprintf(`onebase_limited_operation_total{kind="export.run",reason="concurrency"} %d`, total),
		`onebase_active_operations{kind="report.run"} 0`, // Start/Finish парны → вернулись к нулю
	} {
		if !strings.Contains(out, want) {
			t.Errorf("ожидали %q в выводе:\n%s", want, out)
		}
	}
}

// Незаматченный путь группируется под route="other", а не плодит серию.
func TestRegistry_UnmatchedRouteIsOther(t *testing.T) {
	reg := New()
	r := chi.NewRouter()
	r.Use(reg.Middleware)
	// Хотя бы один обычный маршрут нужен, чтобы chi построил цепочку middleware
	// (иначе при наличии только NotFound запрос идёт мимо Use). В реальном
	// сервере маршруты всегда есть.
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {})
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/no/such/path", nil))

	var sb strings.Builder
	if err := reg.WritePrometheus(&sb); err != nil {
		t.Fatalf("WritePrometheus: %v", err)
	}
	if !strings.Contains(sb.String(), `route="other",status="404"`) {
		t.Errorf("ожидали route=other для незаматченного пути, получили:\n%s", sb.String())
	}
}
