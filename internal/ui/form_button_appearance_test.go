package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestApplicationButtonAppearance_PublicForm(t *testing.T) {
	first := &metadata.FormElement{Kind: metadata.FormElementButton, Name: "КнЗаполнить"}
	second := &metadata.FormElement{Kind: metadata.FormElementButton, Name: "КнУдалитьСтроку"}
	third := &metadata.FormElement{Kind: metadata.FormElementButton, Name: "КнЗаписать"}
	s, entity := setupManagedEventsServer(t, "", nil, []*metadata.FormElement{
		{Kind: metadata.FormElementGroupBox, Name: "Controls", Orientation: "horizontal", Children: []*metadata.FormElement{first, second, third}},
	})
	s.cfg.UIExceptRoles = []string{"ОператорКЦ", "ОператорМессенджера"}
	router := chi.NewRouter()
	s.Mount(router)
	get := func(role string) string {
		t.Helper()
		r := httptest.NewRequest("GET", "/ui/catalog/"+entity.Name+"/new", nil)
		if role != "" {
			u := &auth.User{Login: "operator", Roles: []*auth.Role{{Name: role, Permissions: auth.Permission{
				Catalogs: map[string][]string{entity.Name: {"read", "write"}},
			}}}}
			r = r.WithContext(auth.ContextWithUser(r.Context(), u))
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	body := get("")
	left := strings.Index(body, `class="btn btn-app-secondary managed-btn"`)
	middle := strings.Index(body, `class="btn btn-app-danger managed-btn"`)
	right := strings.Index(body, `class="btn btn-app-primary managed-btn"`)
	if left < 0 || middle < left || right < middle || !strings.Contains(body, `id="main-form" class="ob-form-action-spacing"`) {
		t.Fatal("команды должны различаться стилем и сохранить исходный порядок")
	}
	second.ButtonKind = "secondary"
	body = get("")
	if strings.Contains(body, `class="btn btn-app-danger managed-btn"`) || strings.Count(body, `class="btn btn-app-secondary managed-btn"`) != 2 {
		t.Fatal("метаданные вида кнопки должны переопределять умолчание")
	}
	for _, role := range []string{"ОператорКЦ", "ОператорМессенджера"} {
		body := get(role)
		if strings.Contains(body, `id="main-form" class="ob-form-action-spacing"`) ||
			strings.Count(body, `class="btn btn-secondary managed-btn"`) != 3 {
			t.Fatalf("стиль и отступы оператора изменились: %s", role)
		}
	}
}
