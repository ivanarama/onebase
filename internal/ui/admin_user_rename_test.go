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
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Карточка пользователя в веб-админке: администратор меняет логин тем же
// «Сохранить» (POST /ui/admin/users/{id}). Логин и личная настройка
// переезжают, сессии учётки завершаются; занятый логин — ошибка на форме, и
// тогда не сохраняется ничего; не-администратору карточка недоступна.
func TestAdminUserCard_RenameLogin(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	admin, err := repo.Create(ctx, "admin", "password1", "Администратор", true)
	if err != nil {
		t.Fatal(err)
	}
	op, err := repo.Create(ctx, "ОператорКоллЦентраТест", "password1", "Оператор", false)
	if err != nil {
		t.Fatal(err)
	}
	token, err := repo.CreateSession(ctx, op.ID, auth.SessionMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveUserFormOpenMode(ctx, op.Login, "tabs"); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: db, reg: runtime.NewRegistry(), authRepo: repo, messages: NewMessageStore()}
	router := chi.NewRouter()
	s.Mount(router)
	post := func(as *auth.User, login, fullName string) string {
		t.Helper()
		form := url.Values{"action": {"update"}, "login": {login}, "full_name": {fullName}}
		req := httptest.NewRequest(http.MethodPost, "/ui/admin/users/"+op.ID, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(auth.ContextWithUser(req.Context(), as))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Body.String()
	}

	body := post(admin, "Оператор_075", "Оператор 75")
	if !strings.Contains(body, "Логин изменён") {
		t.Fatalf("нет сообщения о смене логина:\n%s", body)
	}
	got, err := repo.GetByID(ctx, op.ID)
	if err != nil || got.Login != "Оператор_075" || got.FullName != "Оператор 75" {
		t.Fatalf("после сохранения: %+v %v", got, err)
	}
	if u, _ := repo.LookupSession(ctx, token); u != nil {
		t.Fatal("сессии переименованной учётки должны быть завершены")
	}
	if mode := db.GetUserFormOpenMode(ctx, "Оператор_075"); mode != "tabs" {
		t.Fatalf("личная настройка не переехала: %q", mode)
	}

	body = post(admin, "Admin", "Другое имя")
	if !strings.Contains(body, auth.ErrLoginTaken.Error()) {
		t.Fatalf("занятый логин: нет ошибки на форме:\n%s", body)
	}
	if got, _ := repo.GetByID(ctx, op.ID); got.Login != "Оператор_075" || got.FullName != "Оператор 75" {
		t.Fatalf("при ошибке логина ничего не должно сохраниться: %+v", got)
	}

	plain := &auth.User{ID: op.ID, Login: "Оператор_075"}
	post(plain, "взлом", "")
	if got, _ := repo.GetByID(ctx, op.ID); got.Login != "Оператор_075" {
		t.Fatalf("не-администратор переименовал учётку: %+v", got)
	}
}

// Смена логина и остальных полей карточки — одной транзакцией (замечание ревью
// #2364): единственный администратор меняет себе логин и снимает флаг
// администратора — отказ «последний администратор», и логин не меняется,
// сессия не завершается. Раньше логин успевал смениться до отказа.
func TestAdminUserCard_RenameRolledBackWhenCardRejected(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	admin, err := repo.Create(ctx, "admin", "password1", "Администратор", true)
	if err != nil {
		t.Fatal(err)
	}
	token, err := repo.CreateSession(ctx, admin.ID, auth.SessionMeta{})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: db, reg: runtime.NewRegistry(), authRepo: repo, messages: NewMessageStore()}
	router := chi.NewRouter()
	s.Mount(router)
	form := url.Values{"action": {"update"}, "login": {"admin2"}, "full_name": {"Новое имя"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/admin/users/"+admin.ID, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.ContextWithUser(req.Context(), admin))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), auth.ErrLastAdmin.Error()) {
		t.Fatalf("нет отказа «последний администратор»:\n%s", rec.Body.String())
	}
	got, err := repo.GetByID(ctx, admin.ID)
	if err != nil || got.Login != "admin" || got.FullName != "Администратор" || !got.IsAdmin {
		t.Fatalf("отказ сохранения применил часть изменений: %+v %v", got, err)
	}
	if u, _ := repo.LookupSession(ctx, token); u == nil {
		t.Fatal("отказ сохранения завершил сессию администратора")
	}
}
