package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/project"
	"golang.org/x/net/html"
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
func refCardProject(t *testing.T, formKeys, elementKeys string, local ...bool) *project.Project {
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
			refCardFormElements(entity, local, elementKeys)
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

// Both branches are loaded from YAML rather than calling the template helper.
func refCardFormElements(entity string, local []bool, keys string) string {
	attributes, path := "", "Объект.Клиент"
	if entity == "Заказ" && len(local) > 0 && local[0] {
		attributes = "attributes:\n  - {name: ВыбранныйКлиент, type: CatalogRef.Клиент, save: false}\n"
		path = "Форма.ВыбранныйКлиент"
	}
	return attributes + "elements:\n  - kind: ПолеВвода\n    name: ПолеКлиент\n    data_path: " + path + "\n" + keys
}

func refCardControl(t *testing.T, body string) *html.Node {
	t.Helper()
	anchor := managedAnchorNode(t, body, "ПолеКлиент")
	if anchor == nil {
		t.Fatal("form has no ПолеКлиент element")
	}
	if refCardNode(anchor, "select", "data-ref-entity") == nil {
		t.Fatal("element has no reference select")
	}
	return anchor
}

func refCardNode(root *html.Node, tag, attr string) *html.Node {
	if root == nil {
		return nil
	}
	if root.Type == html.ElementNode && root.Data == tag {
		if _, ok := managedHTMLAttr(root, attr); ok {
			return root
		}
	}
	for n := root.FirstChild; n != nil; n = n.NextSibling {
		if found := refCardNode(n, tag, attr); found != nil {
			return found
		}
	}
	return nil
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
	s, ctx := newSubmitTestServer(t, proj.Entities)
	if err := s.store.Upsert(ctx, "Клиент", uuid.New(), map[string]any{"Наименование": "Клиент"}, s.reg.GetEntity("Клиент")); err != nil {
		t.Fatal(err)
	}
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
		anchor := refCardControl(t, rec.Body.String())
		for _, attr := range []string{"data-ob-ref-picker", "data-ob-ref-current"} {
			if button := refCardNode(anchor, "button", attr); button != nil {
				if value, _ := managedHTMLAttr(button, attr); value != "closest" {
					t.Errorf("%s = %q, want closest", attr, value)
				}
				if attr == "data-ob-ref-current" {
					if value, _ := managedHTMLAttr(button, "data-ob-readonly-navigation"); value != "1" {
						t.Error("card button lost readonly navigation")
					}
				}
			}
		}
		return refCardNode(anchor, "button", "data-ob-ref-current") != nil
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
		{name: "readonly empty hides navigation", elementKeys: "    readonly: true\n    ref_card_button: true\n", user: admin, withAuth: true, want: false},
		// Без настроенной авторизации платформа работает в административном
		// режиме (isAdminCtx): «только администратору» = всем.
		{name: "без авторизации — административный режим",
			formKeys: "  ref_card_button_admin_only: true\n", withAuth: false, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, local := range []bool{false, true} {
				name := "entity-field"
				if local {
					name = "form-attribute"
				}
				t.Run(name, func(t *testing.T) {
					proj := refCardProject(t, tc.formKeys, tc.elementKeys, local)
					document, processor := refCardButtonsFor(t, proj, tc.user, tc.withAuth)
					if document != tc.want {
						t.Errorf("document card = %v, want %v", document, tc.want)
					}
					if processor != tc.want {
						t.Errorf("processor card = %v, want %v", processor, tc.want)
					}
				})
			}
		})
	}
}
