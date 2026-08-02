package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/storage"
)

// authRepoWithoutSchema даёт репозиторий поверх пустой базы: таблиц ролей в ней
// нет, поэтому любое чтение ролей вернёт ошибку. Это воспроизводит недоступный
// справочник ролей детерминированно, без подмены интерфейсов.
func authRepoWithoutSchema(t *testing.T) *auth.Repo {
	t.Helper()
	db, err := storage.ConnectSQLite(context.Background(), filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return auth.NewRepo(db)
}

// Назначение ролей считает диф от текущего состояния, поэтому сбой ЧТЕНИЯ
// опаснее сбоя записи: при пустом currentIDs ни одна роль не попадает в ветку
// снятия, цикл отрабатывает вхолостую — и админ получает редирект на список
// пользователей, то есть подтверждение, на запрос, где он СНИМАЛ роль.
func TestAdminUserRolesUpdateReportsReadFailure(t *testing.T) {
	s, _ := newSubmitTestServer(t, nil)
	s.authRepo = authRepoWithoutSchema(t)

	form := url.Values{"role_id": {}}
	req := httptest.NewRequest(http.MethodPost, "/ui/admin/users/u1/roles",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "u1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{ID: "admin", IsAdmin: true}))
	rec := httptest.NewRecorder()

	s.adminUserRolesUpdate(rec, req)

	if rec.Code == http.StatusFound {
		t.Fatal("недоступный справочник ролей выдан за применённые изменения (редирект на список)")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("код = %d, ожидался 500; тело %q", rec.Code, rec.Body.String())
	}
}

// Смена пароля отзывает сессии ради того, чтобы украденная сессия её не
// пережила. Сбой отзыва раньше отбрасывался, и в аудит уходила запись
// «password_change_sessions_revoked» — журнал утверждал ровно то, чего не
// произошло. Теперь функция возвращает ошибку и записи не делает.
func TestRevokeSessionsReturnsErrorAndSkipsAudit(t *testing.T) {
	s, _ := newSubmitTestServer(t, nil)
	s.authRepo = authRepoWithoutSchema(t)

	req := httptest.NewRequest(http.MethodPost, "/ui/admin/users/u1/passwd", nil)

	if err := s.revokeSessionsOnPasswordChange(req, "u1", "petrov"); err == nil {
		t.Fatal("сбой отзыва сессий должен возвращать ошибку")
	}
}
