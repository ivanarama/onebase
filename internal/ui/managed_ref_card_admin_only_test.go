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
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/project"
)

// ref_card_button_admin_only (#1876): кнопку «Открыть карточку» (🔍) у
// ссылочных полей видит только администратор; явный ref_card_button поля
// сильнее обоих ключей формы. Тест идёт пользовательским путём: YAML проекта →
// project.Load → production-маршрут GET формы документа и формы обработки →
// итоговый HTML. Пользователь лежит в контексте запроса так же, как его кладёт
// middleware авторизации приложения.

const refCardProcessor = "ПодборКлиента"

// refCardProject пишет проект с документом Заказ и обработкой ПодборКлиента,
// у обеих — управляемая форма с одним ссылочным полем Клиент.
func refCardProject(t *testing.T, formKeys, elementKeys string) *project.Project {
	t.Helper()
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
	write("catalogs/клиент.yaml", "name: Клиент\nfields:\n  - name: Наименование\n    type: string\n")
	write("documents/заказ.yaml", "name: Заказ\nfields:\n  - name: Клиент\n    type: reference:Клиент\n")
	write("processors/подборклиента.yaml", "name: "+refCardProcessor+"\nparams:\n  - {name: Клиент, type: \"reference:Клиент\"}\n")
	form := func(entity, name string) string {
		return "schema: onebase.form/v1\nform:\n  name: " + name + "\n  kind: object\n  entity: " + entity + "\n" + formKeys +
			"elements:\n  - kind: ПолеВвода\n    name: ПолеКлиент\n    data_path: Объект.Клиент\n" + elementKeys
	}
	write("forms/заказ/объекта.form.yaml", form("Заказ", "ФормаОбъекта"))
	write("forms/подборклиента/обработки.form.yaml", form(refCardProcessor, "ФормаОбработки"))
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Cleanup(func() { proj.Close() })
	return proj
}

// refCardOperator — обычный пользователь: читает и пишет заказы, читает
// клиентов (без этого поле не заполнить), запускает обработку.
func refCardOperator() *auth.User {
	return &auth.User{Login: "operator", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs:   map[string][]string{"Клиент": {"read"}},
		Documents:  map[string][]string{"Заказ": {"read", "write"}},
		Processors: map[string][]string{refCardProcessor: {"run"}},
	}}}}
}

// refCardButtonsFor отдаёт наличие кнопки у формы документа и формы обработки.
// user == nil — запрос без пользователя; withAuth=false — база без настроенной
// авторизации.
func refCardButtonsFor(t *testing.T, proj *project.Project, user *auth.User, withAuth bool) (document, processor bool) {
	t.Helper()
	s, _ := newSubmitTestServer(t, proj.Entities)
	s.reg.LoadProcessors(proj.Processors)
	if withAuth {
		s.authRepo = auth.NewRepo(s.store)
	}
	router := chi.NewRouter()
	s.Mount(router)
	get := func(path string) bool {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if user != nil {
			req = req.WithContext(auth.ContextWithUser(req.Context(), user))
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s → %d: %.600s", path, rec.Code, rec.Body.String())
		}
		html := rec.Body.String()
		if !strings.Contains(html, `id="ref-Клиент"`) {
			t.Fatalf("GET %s: в форме нет ссылочного поля Клиент:\n%.1200s", path, html)
		}
		return strings.Contains(html, `data-ob-ref-current="ref-Клиент"`)
	}
	return get("/ui/document/" + url.PathEscape("Заказ") + "/new"),
		get("/ui/processor/" + url.PathEscape(refCardProcessor))
}

func TestRefCardButtonAdminOnlyThroughHTTP(t *testing.T) {
	admin := &auth.User{Login: "root", IsAdmin: true}
	for _, tc := range []struct {
		name        string
		formKeys    string
		elementKeys string
		user        *auth.User
		withAuth    bool
		want        bool
	}{
		{name: "ключей нет — кнопка у всех", user: refCardOperator(), withAuth: true, want: true},
		{name: "только администратору: администратор видит",
			formKeys: "  ref_card_button_admin_only: true\n", user: admin, withAuth: true, want: true},
		{name: "только администратору: оператор не видит",
			formKeys: "  ref_card_button_admin_only: true\n", user: refCardOperator(), withAuth: true, want: false},
		{name: "только администратору: явный true поля сильнее",
			formKeys: "  ref_card_button_admin_only: true\n", elementKeys: "    ref_card_button: true\n",
			user: refCardOperator(), withAuth: true, want: true},
		{name: "только администратору: явный false поля прячет и от администратора",
			formKeys: "  ref_card_button_admin_only: true\n", elementKeys: "    ref_card_button: false\n",
			user: admin, withAuth: true, want: false},
		{name: "ref_card_button: false формы прячет и от администратора",
			formKeys: "  ref_card_button: false\n  ref_card_button_admin_only: true\n", user: admin, withAuth: true, want: false},
		{name: "ref_card_button: false формы, явный true поля",
			formKeys: "  ref_card_button: false\n", elementKeys: "    ref_card_button: true\n",
			user: refCardOperator(), withAuth: true, want: true},
		{name: "ключей формы нет, явный false поля",
			elementKeys: "    ref_card_button: false\n", user: admin, withAuth: true, want: false},
		// Без настроенной авторизации платформа работает в административном
		// режиме (isAdminCtx): «только администратору» = всем.
		{name: "без авторизации — административный режим",
			formKeys: "  ref_card_button_admin_only: true\n", withAuth: false, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proj := refCardProject(t, tc.formKeys, tc.elementKeys)
			document, processor := refCardButtonsFor(t, proj, tc.user, tc.withAuth)
			if document != tc.want {
				t.Errorf("форма документа: кнопка «Открыть карточку» = %v, ожидалось %v", document, tc.want)
			}
			if processor != tc.want {
				t.Errorf("форма обработки: кнопка «Открыть карточку» = %v, ожидалось %v", processor, tc.want)
			}
		})
	}
}
