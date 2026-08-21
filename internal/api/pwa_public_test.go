package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
)

// newServerWithUser строит реальный API-сервер и создаёт одного пользователя,
// чтобы HasUsers==true и auth-мидлвара начала гейтить защищённые маршруты.
func newServerWithUser(t *testing.T) *Server {
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
	if _, err := authRepo.Create(ctx, "admin", "secret123", "Админ", true); err != nil {
		t.Fatal(err)
	}
	if has, err := authRepo.HasUsers(ctx); err != nil || !has {
		t.Fatalf("HasUsers=%v err=%v, ожидалось true", has, err)
	}
	return newTestServer(runtime.NewRegistry(), db, interpreter.New(), authRepo, "", 0, ui.Config{}, nil)
}

// TestPWAAssetsPublicWhenUsersExist: ассеты PWA доступны БЕЗ аутентификации,
// даже когда в базе есть пользователи и cookie сессии отсутствует.
//
// Браузер запрашивает manifest без credentials, а install-промпт и иконки
// фечатся вне контекста страницы. Если ассеты под auth-мидлварой, PWA не
// устанавливается на любом инстансе с пользователями (план 45, ревью PR #34).
func TestPWAAssetsPublicWhenUsersExist(t *testing.T) {
	srv := newServerWithUser(t)

	routes := []string{
		"/manifest.webmanifest",
		"/sw.js",
		"/offline.html",
		"/icons/icon-192.png",
		"/icons/icon-512.png",
	}
	for _, path := range routes {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("неаутентифицированный GET %s = %d, ожидался 200 (ассет PWA должен быть публичным)", path, rec.Code)
			}
		})
	}
}

// TestProtectedRoutesStillGated — контроль: вынос PWA в публичные маршруты не
// должен открыть бизнес-страницы. /ui/ без сессии остаётся за auth.
func TestProtectedRoutesStillGated(t *testing.T) {
	srv := newServerWithUser(t)

	req := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Errorf("неаутентифицированный GET /ui/ = 200, ожидался редирект/401 (маршрут должен быть под auth)")
	}
}
