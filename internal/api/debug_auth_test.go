package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
)

func newDebugTestServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	authRepo := auth.NewRepo(db)
	if err := authRepo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	return newTestServer(runtime.NewRegistry(), db, interpreter.New(), authRepo, "", 0, ui.Config{}, nil)
}

// Без ONEBASE_DEBUG_TOKEN debug-маршруты не регистрируются вовсе —
// опубликованная база (`onebase run`) не имеет debug-поверхности.
func TestDebugAPI_AbsentWithoutToken(t *testing.T) {
	_ = os.Unsetenv("ONEBASE_DEBUG_TOKEN")
	srv := newDebugTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/debug/global/enable", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("без токена ожидался 404 (роут отсутствует), получено %d", rec.Code)
	}
}

// С заданным токеном debug-эндпоинт требует совпадающий X-OneBase-Debug-Token.
func TestDebugAPI_TokenEnforced(t *testing.T) {
	_ = os.Setenv("ONEBASE_DEBUG_TOKEN", "s3cr3t")
	t.Cleanup(func() { _ = os.Unsetenv("ONEBASE_DEBUG_TOKEN") })
	srv := newDebugTestServer(t)

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"без заголовка", "", http.StatusUnauthorized},
		{"неверный токен", "wrong", http.StatusUnauthorized},
		{"верный токен", "s3cr3t", http.StatusOK},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/debug/global/enable", nil)
		if c.header != "" {
			req.Header.Set("X-OneBase-Debug-Token", c.header)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: ожидался %d, получено %d", c.name, c.want, rec.Code)
		}
	}
}
