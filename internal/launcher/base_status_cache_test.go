package launcher

import (
	"net"
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
