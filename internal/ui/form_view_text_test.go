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
	"github.com/ivantit66/onebase/internal/project"
	"golang.org/x/net/html"
)

// ПолеВвода view: text / text-strong — значение реквизита текстом с подписью
// сверху (как поля прежнего диалога 1С), и actions.cancel.visible: false у
// формы обработки. Тест идёт пользовательским путём: YAML проекта →
// project.Load → production-маршруты GET формы документа и формы обработки.
func viewTextProject(t *testing.T, processorActions string) *project.Project {
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
	write("documents/заказ.yaml", "name: Заказ\nfields:\n  - name: Номер\n    type: string\n  - name: Примечание\n    type: string\n")
	write("forms/заказ/объекта.form.yaml", `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ПолеВвода
    name: ПолеНомер
    data_path: Объект.Номер
    view: text-strong
    title: {ru: Постоянный номер заявки}
  - kind: ПолеВвода
    name: ПолеПримечание
    data_path: Объект.Примечание
    view: text
    title: {ru: Примечание к заказчику}
`)
	write("processors/итог.yaml", "name: Итог\n")
	write("forms/итог/обработки.form.yaml", "schema: onebase.form/v1\nform:\n  name: ФормаОбработки\n  kind: object\n  entity: Итог\n"+processorActions+
		"elements:\n  - kind: Кнопка\n    name: Закрыть\n    title: {ru: Закрыть}\n")
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Cleanup(func() { proj.Close() })
	return proj
}

func TestFormViewTextThroughHTTP(t *testing.T) {
	proj := viewTextProject(t, "")
	s, ctx := newSubmitTestServer(t, proj.Entities)
	s.reg.LoadProcessors(proj.Processors)
	id := uuid.New()
	note := "первая строка\n<b>не разметка</b>"
	if err := s.store.Upsert(ctx, "Заказ", id, map[string]any{"Номер": "Пл7993f97485", "Примечание": note}, s.reg.GetEntity("Заказ")); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	s.Mount(router)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/document/"+url.PathEscape("Заказ")+"/"+id.String(), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET form: %d %.600s", rec.Code, rec.Body.String())
	}
	doc, err := html.Parse(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ element, field, label, value, weight string }{
		{"ПолеНомер", "Номер", "Постоянный номер заявки", "Пл7993f97485", "font-weight:700"},
		{"ПолеПримечание", "Примечание", "Примечание к заказчику", note, ""},
	} {
		wrap := managedAnchorNode(t, rec.Body.String(), tc.element)
		if wrap == nil {
			t.Fatalf("%s: no element wrapper", tc.element)
		}
		value := refCardNode(wrap, "div", "data-ob-value")
		if value == nil {
			t.Fatalf("%s: no data-ob-value node", tc.element)
		}
		if got, _ := managedHTMLAttr(value, "data-ob-value"); got != tc.field {
			t.Fatalf("%s: data-ob-value=%q", tc.element, got)
		}
		if got := nodeText(value); got != tc.value {
			t.Fatalf("%s: value text %q, want %q", tc.element, got, tc.value)
		}
		style, _ := managedHTMLAttr(value, "style")
		if !strings.Contains(style, "pre-wrap") || (tc.weight != "") != strings.Contains(style, "font-weight:700") {
			t.Fatalf("%s: value style %q", tc.element, style)
		}
		if !strings.Contains(nodeText(wrap), tc.label) {
			t.Fatalf("%s: label %q missing", tc.element, tc.label)
		}
		// No editable control; the value travels back unchanged in a hidden input.
		for _, tag := range []string{"textarea", "select"} {
			if refCardNode(wrap, tag, "name") != nil {
				t.Fatalf("%s: editable %s rendered", tc.element, tag)
			}
		}
		input := refCardNode(wrap, "input", "name")
		if input == nil {
			t.Fatalf("%s: no hidden input", tc.element)
		}
		if typ, _ := managedHTMLAttr(input, "type"); typ != "hidden" {
			t.Fatalf("%s: input type %q, want hidden", tc.element, typ)
		}
		if v, _ := managedHTMLAttr(input, "value"); v != tc.value {
			t.Fatalf("%s: hidden value %q", tc.element, v)
		}
	}
	if strings.Contains(rec.Body.String(), "<b>не разметка</b>") {
		t.Fatal("value is not escaped")
	}
	_ = doc
}

func TestProcessorCancelActionVisibilityThroughHTTP(t *testing.T) {
	for _, tc := range []struct {
		name, actions string
		want          bool
	}{
		{"по умолчанию «Отмена» есть", "", true},
		{"cancel.visible: false скрывает", "actions:\n  cancel: {visible: false}\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proj := viewTextProject(t, tc.actions)
			s, _ := newSubmitTestServer(t, proj.Entities)
			s.reg.LoadProcessors(proj.Processors)
			router := chi.NewRouter()
			s.Mount(router)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/processor/"+url.PathEscape("Итог"), nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("GET processor: %d %.600s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			got := strings.Contains(body, `class="btn btn-cancel">Отмена</a>`)
			if got != tc.want {
				t.Fatalf("footer «Отмена» present=%t, want %t", got, tc.want)
			}
			// The header cross still closes the form.
			if !strings.Contains(body, "data-ob-close-tab") {
				t.Fatal("form lost its close control")
			}
		})
	}
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}
