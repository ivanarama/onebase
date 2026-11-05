package ui

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Страница «Администрирование → Пользователи»: таблица из семи колонок с тремя
// кнопками действий шире карточки max-width:800px и вылезала за неё. Таблица
// обязана лежать в прокручиваемой обёртке внутри карточки, кнопки — в одну
// строку. Через публичный GET /ui/admin/users.
func TestAdminUsersTableStaysInsideCard(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	authRepo := auth.NewRepo(db)
	if err := authRepo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	admin, err := authRepo.Create(ctx, "admin", "password1", "Администратор", true)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: db, reg: runtime.NewRegistry(), authRepo: authRepo, messages: NewMessageStore()}
	router := chi.NewRouter()
	s.Mount(router)

	req := httptest.NewRequest("GET", "/ui/admin/users", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), admin))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("код %d", rec.Code)
	}
	body := rec.Body.String()

	if !regexp.MustCompile(`\.table-wrap\{[^}]*overflow-x:auto`).MatchString(body) {
		t.Fatal("нет правила прокрутки .table-wrap")
	}
	if !strings.Contains(body, `<div class="table-wrap"><table>`) {
		t.Fatal("таблица пользователей не обёрнута в прокручиваемый контейнер")
	}
	if strings.Contains(body, `class="card" style="max-width:800px"`) {
		t.Fatal("карточка пользователей по-прежнему ограничена 800px")
	}
	if !strings.Contains(body, "flex-wrap:nowrap;white-space:nowrap") {
		t.Fatal("кнопки действий могут переноситься по одной")
	}
}
