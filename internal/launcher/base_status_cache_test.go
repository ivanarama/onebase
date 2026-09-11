package launcher

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// freePort грабит и сразу отпускает порт, который наверняка свободен — тогда
// baseRunning коротко замыкается на portFree и не бьёт /health (быстрый тест).
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// Статус базы кэшируется на короткий TTL: быстрое переключение списка не должно
// перепробовать /health и открывать БД на каждый рендер (issue #596). А
// мутирующее действие (invalidateStatus) обязано заставить перепробовать сразу.
func TestBaseStatusesCacheAndInvalidate(t *testing.T) {
	h := &handler{runner: NewRunner()}
	b := &Base{ID: "x", Port: freePort(t), ConfigSource: "file", Path: t.TempDir()}

	fetched := func() time.Time {
		h.statusMu.Lock()
		defer h.statusMu.Unlock()
		return h.statusCache["x"].fetched
	}

	h.baseStatuses([]*Base{b})
	t0 := fetched()
	if t0.IsZero() {
		t.Fatal("статус не закэширован после первого запроса")
	}

	// Повтор в пределах TTL — из кэша, без перепробы.
	h.baseStatuses([]*Base{b})
	if t1 := fetched(); !t1.Equal(t0) {
		t.Fatalf("статус перепробован в пределах TTL — кэш не работает (%v → %v)", t0, t1)
	}

	// Инвалидация заставляет перепробовать.
	h.invalidateStatus("x")
	time.Sleep(time.Millisecond) // гарантируем отличимый fetched на грубых часах
	h.baseStatuses([]*Base{b})
	if t2 := fetched(); t2.Equal(t0) {
		t.Fatal("после инвалидации статус не перепробован")
	}
}

// HTTP-список не должен ждать сетевого таймаута PostgreSQL: медленная база
// получает короткий UI-deadline, не мешая параллельно проверить остальные
// строки. Повторный рендер берёт оба результата из кэша.
func TestIndex_DatabaseStatusProbeRespectsDeadline(t *testing.T) {
	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	for _, b := range []*Base{
		{ID: "slow", Name: "Недоступная", ConfigSource: "database", Port: freePort(t)},
		{ID: "fast", Name: "Доступная", ConfigSource: "database", Port: freePort(t)},
	} {
		if err := store.Add(b); err != nil {
			t.Fatal(err)
		}
	}

	fastDone := make(chan struct{})
	var calls atomic.Int32
	var ranInParallel atomic.Bool
	h := &handler{
		store:         store,
		runner:        NewRunner(),
		statusTimeout: 40 * time.Millisecond,
		statusReadAppYAML: func(ctx context.Context, b *Base, _ any) error {
			calls.Add(1)
			if b.ID == "fast" {
				close(fastDone)
				return nil
			}
			select {
			case <-fastDone:
				ranInParallel.Store(true)
			case <-ctx.Done():
				return ctx.Err()
			}
			<-ctx.Done()
			return ctx.Err()
		},
	}

	render := func() (time.Duration, *httptest.ResponseRecorder) {
		t.Helper()
		start := time.Now()
		rec := httptest.NewRecorder()
		h.index(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		return time.Since(start), rec
	}

	elapsed, rec := render()
	if rec.Code != http.StatusOK {
		t.Fatalf("index status=%d body=%s", rec.Code, rec.Body.String())
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("список ждал недоступную базу %s", elapsed)
	}
	if !ranInParallel.Load() {
		t.Fatal("быстрая база не проверилась параллельно с зависшей")
	}
	for _, want := range []string{"Недоступная", "Доступная"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("в списке нет %q", want)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("первый рендер выполнил %d проб вместо 2", got)
	}

	if elapsed, rec = render(); rec.Code != http.StatusOK || elapsed > 500*time.Millisecond {
		t.Fatalf("повторный index: status=%d elapsed=%s", rec.Code, elapsed)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("кэш не сработал: проб=%d, ожидалось 2", got)
	}
}
