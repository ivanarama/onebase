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
	"golang.org/x/net/html"
)

// admin_only: руководство закрыто всем, кроме администратора. Кнопка
// «? Справка» в формах остаётся на месте, но ссылку получает только
// администратор; остальным — недоступная кнопка без href, пункта меню
// «Руководство пользователя» нет, а /ui/help, главы и картинки отвечают 403. Тест идёт пользовательским путём: docs/help/index.yaml проекта →
// project.Load → production-маршруты GET управляемой формы документа, формы
// обработки и автоформы справочника → итоговый HTML. Пользователь лежит в
// контексте запроса так же, как его кладёт middleware авторизации.

func helpAdminOnlyProject(t *testing.T, option string) *project.Project {
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
		return "schema: onebase.form/v1\nform:\n  name: " + name + "\n  kind: object\n  entity: " + entity + "\n" +
			"elements:\n  - kind: ПолеВвода\n    name: ПолеКлиент\n    data_path: Объект.Клиент\n"
	}
	write("forms/заказ/объекта.form.yaml", form("Заказ", "ФормаОбъекта"))
	write("forms/подборклиента/обработки.form.yaml", form(refCardProcessor, "ФормаОбработки"))
	write("docs/help/index.yaml", "title: Руководство\n"+option+"chapters:\n"+
		"  - id: orders\n    title: Заказы\n    file: orders.md\n    objects: [document/Заказ, processor/"+refCardProcessor+", catalog/Клиент]\n")
	write("docs/help/orders.md", "# Заказы\n\n![Форма](images/order.png)\n")
	write("docs/help/images/order.png", "image-bytes")
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Cleanup(func() { proj.Close() })
	if len(proj.Help.Warnings) != 0 {
		t.Fatalf("help warnings: %v", proj.Help.Warnings)
	}
	return proj
}

func findContextHelp(n *html.Node) *html.Node {
	if n.Type == html.ElementNode {
		if _, ok := managedHTMLAttr(n, "data-ob-context-help"); ok {
			return n
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findContextHelp(c); found != nil {
			return found
		}
	}
	return nil
}

// helpOperator — обычный пользователь, которому доступны все три формы.
func helpOperator() *auth.User {
	return &auth.User{Login: "operator", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs:   map[string][]string{"Клиент": {"read", "write"}},
		Documents:  map[string][]string{"Заказ": {"read", "write"}},
		Processors: map[string][]string{refCardProcessor: {"run"}},
	}}}}
}

func TestContextHelpAdminOnlyThroughHTTP(t *testing.T) {
	admin := &auth.User{Login: "root", IsAdmin: true}
	for _, tc := range []struct {
		name     string
		option   string
		user     *auth.User
		withAuth bool
		enabled  bool
	}{
		{name: "опции нет — ссылка у оператора", user: helpOperator(), withAuth: true, enabled: true},
		{name: "опция false — ссылка у оператора", option: "admin_only: false\n", user: helpOperator(), withAuth: true, enabled: true},
		{name: "только администратору: администратор открывает", option: "admin_only: true\n", user: admin, withAuth: true, enabled: true},
		{name: "только администратору: оператору недоступна", option: "admin_only: true\n", user: helpOperator(), withAuth: true, enabled: false},
		// Без настроенной авторизации платформа работает в административном
		// режиме (isAdminCtx): «только администратору» = всем.
		{name: "без авторизации — административный режим", option: "admin_only: true\n", withAuth: false, enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proj := helpAdminOnlyProject(t, tc.option)
			s, _ := newSubmitTestServer(t, proj.Entities)
			s.reg.LoadProcessors(proj.Processors)
			s.reg.LoadHelp(proj.Help)
			if tc.withAuth {
				s.authRepo = auth.NewRepo(s.store)
			}
			router := chi.NewRouter()
			s.Mount(router)
			for _, path := range []string{
				"/ui/document/" + url.PathEscape("Заказ") + "/new",
				"/ui/processor/" + url.PathEscape(refCardProcessor),
				"/ui/catalog/" + url.PathEscape("Клиент") + "/new",
			} {
				t.Run(path, func(t *testing.T) {
					req := httptest.NewRequest(http.MethodGet, path, nil)
					if tc.user != nil {
						req = req.WithContext(auth.ContextWithUser(req.Context(), tc.user))
					}
					rec := httptest.NewRecorder()
					router.ServeHTTP(rec, req)
					if rec.Code != http.StatusOK {
						t.Fatalf("GET %s → %d: %.600s", path, rec.Code, rec.Body.String())
					}
					doc, err := html.Parse(strings.NewReader(rec.Body.String()))
					if err != nil {
						t.Fatal(err)
					}
					button := findContextHelp(doc)
					if button == nil {
						t.Fatal("context help button is missing")
					}
					href, hasHref := managedHTMLAttr(button, "href")
					disabled, _ := managedHTMLAttr(button, "aria-disabled")
					title, _ := managedHTMLAttr(button, "title")
					if tc.enabled {
						if href != "/ui/help/chapter/orders" || disabled != "" {
							t.Fatalf("enabled button: href=%q aria-disabled=%q", href, disabled)
						}
						return
					}
					if hasHref || disabled != "true" || title == "" {
						t.Fatalf("disabled button: href=%q (present=%t) aria-disabled=%q title=%q", href, hasHref, disabled, title)
					}
					if strings.Contains(rec.Body.String(), "/ui/help/chapter/orders") {
						t.Fatal("chapter URL leaked into a non-administrator page")
					}
				})
			}
			// Меню «Система → Руководство пользователя» и прямые адреса.
			get := func(path string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				if tc.user != nil {
					req = req.WithContext(auth.ContextWithUser(req.Context(), tc.user))
				}
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				return rec
			}
			page := get("/ui/document/" + url.PathEscape("Заказ") + "/new").Body.String()
			if got := strings.Contains(page, `href="/ui/help"`); got != tc.enabled {
				t.Fatalf("menu guide link present=%t, want %t", got, tc.enabled)
			}
			want := http.StatusOK
			if !tc.enabled {
				want = http.StatusForbidden
			}
			for _, path := range []string{"/ui/help", "/ui/help?q=" + url.QueryEscape("Заказы"), "/ui/help/chapter/orders", "/ui/help/assets/images/order.png"} {
				rec := get(path)
				if rec.Code != want {
					t.Fatalf("GET %s → %d, want %d", path, rec.Code, want)
				}
				if !tc.enabled && strings.Contains(rec.Body.String(), "image-bytes") {
					t.Fatalf("GET %s leaked help content", path)
				}
			}
		})
	}
}
