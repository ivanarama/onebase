package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestCopyPolicy_PublicRoute(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request.yaml")
	const source = `name: А_Заявка
allow_copy: false
fields:
  - name: Описание
    type: string
`
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	request, err := metadata.LoadFile(path, metadata.KindDocument)
	if err != nil {
		t.Fatal(err)
	}
	catalog := copyTestCatalog()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{request, catalog})
	s.cfg.UIExceptRoles = []string{"ОператорКЦ", "ОператорМессенджера"}

	requestID, catalogID := uuid.New(), uuid.New()
	if err := s.store.Upsert(ctx, request.Name, requestID, map[string]any{"Описание": "Описание заявки"}, request); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Upsert(ctx, catalog.Name, catalogID, map[string]any{"Наименование": "ООО Тест"}, catalog); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	s.Mount(router)
	visit := func(method, path string, form url.Values, user *auth.User, want int) string {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		if form != nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if user != nil {
			r = r.WithContext(auth.ContextWithUser(r.Context(), user))
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, r)
		if rec.Code != want {
			t.Fatalf("%s %s: статус %d, ожидалось %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec.Body.String()
	}
	base := "/ui/document/" + request.Name
	copyURL := base + "/new?copy=" + requestID.String()
	body := visit("GET", base, nil, nil, http.StatusOK)
	if !strings.Contains(body, `title="Недостаточно прав" data-ob-list-command="copy" data-ob-command-denied="1"`) ||
		!strings.Contains(body, `data-ob-row-can-copy=""`) || strings.Contains(body, `F2 F9`) {
		t.Fatal("запрет копирования должен выключить значок, F9 и URL строки")
	}
	body = visit("GET", base+"/"+requestID.String(), nil, nil, http.StatusOK)
	if !strings.Contains(body, `aria-label="Скопировать" type="button" disabled`) || strings.Contains(body, `/new?copy=`) {
		t.Fatal("в карточке заявки нужна серая кнопка без ссылки")
	}
	body = visit("GET", copyURL, nil, nil, http.StatusForbidden)
	if strings.Contains(body, "Описание заявки") {
		t.Fatal("запрещённая копия раскрыла значения исходной заявки")
	}
	visit("POST", base+"/new", url.Values{copySourceFormField: {requestID.String()}, "Описание": {"Клон"}}, nil, http.StatusForbidden)
	visit("GET", base+"/new", nil, nil, http.StatusOK)
	catalogURL := "/ui/catalog/" + catalog.Name + "/new?copy=" + catalogID.String()
	body = visit("GET", catalogURL, nil, nil, http.StatusOK)
	if !strings.Contains(body, "ООО Тест") {
		t.Fatal("обычный справочник должен открывать предзаполненную копию")
	}
	readOnly := &auth.User{Login: "test-reader", Roles: []*auth.Role{{Name: "Reader", Permissions: auth.Permission{
		Catalogs: map[string][]string{catalog.Name: {"read"}},
	}}}}
	body = visit("GET", "/ui/catalog/"+catalog.Name, nil, readOnly, http.StatusOK)
	if !strings.Contains(body, `title="Недостаточно прав" data-ob-list-command="copy" data-ob-command-denied="1"`) {
		t.Fatal("у читающей роли нужна серая кнопка")
	}
	visit("GET", catalogURL, nil, readOnly, http.StatusForbidden)
	for _, name := range []string{"ОператорКЦ", "ОператорМессенджера"} {
		operator := &auth.User{Login: "operator", Roles: []*auth.Role{{Name: name, Permissions: auth.Permission{
			Documents: map[string][]string{request.Name: {"read", "write", "create"}},
		}}}}
		body = visit("GET", base+"/"+requestID.String(), nil, operator, http.StatusOK)
		if strings.Contains(body, `class="entity-command-group"`) || !strings.Contains(body, `/new?copy=`) {
			t.Fatalf("прежняя форма роли %s изменилась", name)
		}
		visit("GET", copyURL, nil, operator, http.StatusOK)
	}
}
