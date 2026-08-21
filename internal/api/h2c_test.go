package api

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/net/http2"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
)

// serveH2CTestServer поднимает реальный listener и обслуживает сервер до конца
// теста, возвращая host:port. ONEBASE_H2C нужно выставить ДО вызова: New читает
// его при конструировании (см. h2cEnabled).
func serveH2CTestServer(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "h2c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	authRepo := auth.NewRepo(db)
	if err := authRepo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(runtime.NewRegistry(), db, interpreter.New(), authRepo, "127.0.0.1", 0, ui.Config{}, nil)
	t.Cleanup(srv.frontend.(*ui.Server).Close)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.srv.Serve(ln) }()
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	})
	return ln.Addr().String()
}

// h2cClient — HTTP/2-клиент по cleartext (prior knowledge): AllowHTTP + Dial без
// TLS, так что транспорт сразу шлёт HTTP/2-преамбулу поверх обычного TCP.
func h2cClient() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
		},
	}
}

// При ONEBASE_H2C=1 listener обслуживает HTTP/2 без TLS: h2c-клиент получает
// ответ по HTTP/2 (план 111, P2-1).
func TestH2CUpstreamEnabled(t *testing.T) {
	t.Setenv("ONEBASE_H2C", "1")
	addr := serveH2CTestServer(t)

	resp, err := h2cClient().Get("http://" + addr + "/health")
	if err != nil {
		t.Fatalf("h2c GET /health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.ProtoMajor != 2 {
		t.Fatalf("proto = %q, ожидали HTTP/2", resp.Proto)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, ожидали 200", resp.StatusCode)
	}
}

// Без ONEBASE_H2C сервер остаётся HTTP/1.1-only: тот же h2c-клиент не должен
// получить HTTP/2 (ошибка протокола или даунгрейд), т.е. дефолт не меняется.
func TestH2CUpstreamDisabledByDefault(t *testing.T) {
	t.Setenv("ONEBASE_H2C", "") // детерминизм при унаследованном окружении CI
	addr := serveH2CTestServer(t)

	resp, err := h2cClient().Get("http://" + addr + "/health")
	if err == nil {
		defer func() { _ = resp.Body.Close() }()
		if resp.ProtoMajor == 2 {
			t.Fatal("сервер отдал HTTP/2, хотя h2c выключен")
		}
	}
}
