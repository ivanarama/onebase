package ui

import (
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

// Константы — только администратору: прав на них в ролях нет, а хранят в них
// адреса интеграций и ключи внешних сервисов. Проверяем через маршрутизатор,
// как приходит запрос из браузера.
func TestConstants_NonAdminForbidden(t *testing.T) {
	s, ctx, _ := newConstantsServer(t)
	authDB, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { authDB.Close() })
	s.authRepo = auth.NewRepo(authDB)
	if err := s.authRepo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetConstant(ctx, "Комментарий", "КЛЮЧ-ИНТЕГРАЦИИ"); err != nil {
		t.Fatal(err)
	}

	router := chi.NewRouter()
	s.Mount(router)
	do := func(method string, u *auth.User, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/ui/constants", strings.NewReader(form.Encode()))
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		req = req.WithContext(auth.ContextWithUser(req.Context(), u))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	operator := &auth.User{ID: "op", Login: "operator"}
	admin := &auth.User{ID: "adm", Login: "admin", IsAdmin: true}

	if w := do(http.MethodGet, operator, nil); w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "КЛЮЧ-ИНТЕГРАЦИИ") {
		t.Fatalf("GET не-администратором: %d, значение видно=%v", w.Code, strings.Contains(w.Body.String(), "КЛЮЧ-ИНТЕГРАЦИИ"))
	}
	if w := do(http.MethodPost, operator, url.Values{"ТипКооператива": {"СТ"}, "Комментарий": {"подмена"}}); w.Code != http.StatusForbidden {
		t.Fatalf("POST не-администратором: %d", w.Code)
	}
	if v, _ := s.store.GetConstant(ctx, "Комментарий"); v != "КЛЮЧ-ИНТЕГРАЦИИ" {
		t.Fatalf("не-администратор переписал константу: %v", v)
	}

	if w := do(http.MethodGet, admin, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "КЛЮЧ-ИНТЕГРАЦИИ") {
		t.Fatalf("GET администратором: %d", w.Code)
	}
}
