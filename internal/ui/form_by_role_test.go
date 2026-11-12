package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/project"
)

// form.except_roles / form.roles: у документа две формы объекта — обычная и
// компактная «для всех, кроме ОператорКЦ». Отрисовка и событие формы выбирают
// одну и ту же форму по ролям пользователя, порядок файлов не важен. Тест —
// YAML проекта → project.Load → GET /ui/document/Задача/new и POST form-event.
func TestManagedFormByRoleThroughHTTP(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // G703: rel — литерал теста, каталог — t.TempDir()
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil { //nolint:gosec // G703: rel — литерал теста, каталог — t.TempDir()
			t.Fatal(err)
		}
	}
	write("documents/задача.yaml", "name: Задача\nfields:\n  - name: Описание\n    type: string\n")
	// Компактная форма лежит раньше по алфавиту: выбор не зависит от порядка.
	write("forms/задача/акомпактная.form.yaml", `schema: onebase.form/v1
form:
  name: Компактная
  kind: object
  entity: Задача
  except_roles: [ОператорКЦ]
elements:
  - kind: ПолеВвода
    name: ПолеКомпактное
    data_path: Объект.Описание
    events: {ПриИзменении: КомпактноеИзменено}
`)
	write("forms/задача/акомпактная.form.os", "Процедура КомпактноеИзменено()\n    Сообщить(\"компактная\");\nКонецПроцедуры\n")
	write("forms/задача/объекта.form.yaml", `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Задача
elements:
  - kind: ПолеВвода
    name: ПолеОбычное
    data_path: Объект.Описание
    events: {ПриИзменении: ОбычноеИзменено}
`)
	write("forms/задача/объекта.form.os", "Процедура ОбычноеИзменено()\n    Сообщить(\"обычная\");\nКонецПроцедуры\n")
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Cleanup(func() { proj.Close() })
	s, _ := newSubmitTestServer(t, proj.Entities)
	router := chi.NewRouter()
	s.Mount(router)

	user := func(role string) *auth.User {
		return &auth.User{Login: role, Roles: []*auth.Role{{Name: role, Permissions: auth.Permission{
			Documents: map[string][]string{"Задача": {"read", "write"}},
		}}}}
	}
	withUser := func(req *http.Request, u *auth.User) *http.Request {
		if u == nil {
			return req
		}
		return req.WithContext(auth.ContextWithUser(req.Context(), u))
	}
	for _, tc := range []struct {
		name    string
		user    *auth.User
		element string
		message string
	}{
		{"ОператорКЦ — обычная форма", user("ОператорКЦ"), "ПолеОбычное", "обычная"},
		{"другая роль — компактная форма", user("СотрудникОТК"), "ПолеКомпактное", "компактная"},
		{"роль в другом регистре тоже исключена", user("операторкц"), "ПолеОбычное", "обычная"},
		{"без пользователя — обычная форма", nil, "ПолеОбычное", "обычная"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, withUser(httptest.NewRequest(http.MethodGet, "/ui/document/"+url.PathEscape("Задача")+"/new", nil), tc.user))
			if rec.Code != http.StatusOK {
				t.Fatalf("GET: %d %.400s", rec.Code, rec.Body.String())
			}
			page := rec.Body.String()
			if !strings.Contains(page, `data-ob-el="`+tc.element+`"`) {
				t.Fatalf("form element %s not rendered", tc.element)
			}
			other := map[string]string{"ПолеОбычное": "ПолеКомпактное", "ПолеКомпактное": "ПолеОбычное"}[tc.element]
			if strings.Contains(page, `data-ob-el="`+other+`"`) {
				t.Fatalf("wrong form rendered: %s present", other)
			}
			body := url.Values{"_element": {tc.element}, "_event": {"ПриИзменении"}, "Описание": {"x"}}
			req := httptest.NewRequest(http.MethodPost, "/ui/document/"+url.PathEscape("Задача")+"/form-event", strings.NewReader(body.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
			ev := httptest.NewRecorder()
			router.ServeHTTP(ev, withUser(req.WithContext(context.Background()), tc.user))
			if ev.Code != http.StatusOK {
				t.Fatalf("form-event: %d %s", ev.Code, ev.Body.String())
			}
			resp := decodeFormEventResponse(t, ev.Body.Bytes())
			if len(resp.Messages) != 1 || resp.Messages[0] != tc.message {
				t.Fatalf("form-event handled by %v, want %q", resp.Messages, tc.message)
			}
		})
	}
}
