package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/net/html"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
)

// Проверяем совместность атрибутов в одном уникальном input из публичного GET,
// а не совпадения в разных местах страницы или поведение браузерного профиля.
func assertManagedMaskedInput(t *testing.T, body, name, mask, value string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var inputs []map[string]string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" {
			attrs := map[string]string{}
			for _, a := range n.Attr {
				attrs[a.Key] = a.Val
			}
			if attrs["type"] == "text" && attrs["name"] == name {
				inputs = append(inputs, attrs)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if len(inputs) != 1 {
		t.Fatalf("ожидалось одно текстовое поле %s, получено %d", name, len(inputs))
	}
	want := map[string]string{"autocomplete": "one-time-code", "value": value, "data-ob-input-mask": mask}
	absent := []string{"inputmode"}
	switch mask {
	case "(000)000-00-00":
		want["placeholder"] = "(___)___-__-__"
		want["inputmode"] = "numeric"
		absent = nil
	case "Пл0000000":
		want["placeholder"] = "Пл_______"
		want["inputmode"] = "numeric"
		absent = nil
	case "XX-***":
		want["placeholder"] = "__-___"
	case "":
		want["autocomplete"] = "off"
		delete(want, "data-ob-input-mask")
		absent = append(absent, "data-ob-input-mask", "placeholder")
	default:
		t.Fatalf("неизвестная тестовая маска %q", mask)
	}
	for attr, expected := range want {
		if got, ok := inputs[0][attr]; !ok || got != expected {
			t.Errorf("%s: %s=%q (есть=%v), ожидалось %q", name, attr, got, ok, expected)
		}
	}
	for _, attr := range absent {
		if got, ok := inputs[0][attr]; ok {
			t.Errorf("%s: лишний атрибут %s=%q", name, attr, got)
		}
	}
}

// Маска сохраняет одновременно autocomplete и подсказку формата, независимо
// от заполненности поля. Без маски остаётся off, как в регрессии #595.
func TestManagedForm_MaskedInputSuppressesBrowserAutofill(t *testing.T) {
	for _, tc := range []struct{ name, mask, filled string }{
		{"phone", "(000)000-00-00", "(495)111-22-33"},
		{"prefix", "Пл0000000", "Пл1234567"},
		{"letters", "XX-***", "AB-C12"},
		{"plain", "", "Обычный текст"},
	} {
		for _, value := range []string{"", tc.filled} {
			t.Run(tc.name+"/"+value, func(t *testing.T) {
				srv, ent := setupManagedEventsServer(t, ``, nil, []*metadata.FormElement{
					{Kind: metadata.FormElementField, Name: "ПолеНаименование", DataPath: "Объект.Наименование", InputMask: tc.mask},
					{Kind: metadata.FormElementField, Name: "ПолеЛокальное", DataPath: "Локальное", InputMask: tc.mask},
				})
				ent.Forms[0].Attributes = []*metadata.FormAttribute{{Name: "Локальное", TypeRef: "string"}}
				id := uuid.New()
				if err := srv.store.Upsert(context.Background(), ent.Name, id, map[string]any{"Наименование": value}, ent); err != nil {
					t.Fatal(err)
				}
				body := renderObjectFormGET(t, srv, ent, id)
				assertManagedMaskedInput(t, body, "Наименование", tc.mask, value)
				assertManagedMaskedInput(t, body, "Локальное", tc.mask, "")
			})
		}
	}
}

// Server.Mount → GET формы обработки: параметры с пустым/заданным default и
// локальные реквизиты проходят обе ветки managed-element. Локальный реквизит
// при первом GET пуст: его значение появляется позднее, в событиях формы.
func TestManagedProcessorForm_MaskedInputAutocompleteThroughHTTP(t *testing.T) {
	for _, tc := range []struct{ name, mask, filled string }{
		{"phone", "(000)000-00-00", "(495)111-22-33"},
		{"prefix", "Пл0000000", "Пл1234567"},
		{"letters", "XX-***", "AB-C12"},
		{"plain", "", "Текст & <значение>"},
	} {
		for _, value := range []string{"", tc.filled} {
			t.Run(tc.name+"/"+value, func(t *testing.T) {
				srv, _ := newSubmitTestServer(t, nil)
				proc := &processor.Processor{
					Name:   "ТелефоннаяОбработка",
					Params: []processor.Param{{Name: "Параметр", Type: "string", Default: value}},
					Forms: []*metadata.FormModule{{
						Name: "ФормаОбработки", Kind: "object", LayoutKind: metadata.FormLayoutManaged,
						Attributes: []*metadata.FormAttribute{{Name: "Локальное", TypeRef: "string"}},
						Elements: []*metadata.FormElement{
							{Kind: metadata.FormElementField, Name: "ПолеПараметр", DataPath: "Объект.Параметр", InputMask: tc.mask},
							{Kind: metadata.FormElementField, Name: "ПолеЛокальное", DataPath: "Локальное", InputMask: tc.mask},
						},
					}},
				}
				srv.reg.LoadProcessors([]*processor.Processor{proc})
				router := chi.NewRouter()
				srv.Mount(router)
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/processor/"+proc.Name, nil))
				if rec.Code != http.StatusOK {
					t.Fatalf("GET формы обработки: статус %d: %s", rec.Code, rec.Body.String())
				}
				assertManagedMaskedInput(t, rec.Body.String(), "Параметр", tc.mask, value)
				assertManagedMaskedInput(t, rec.Body.String(), "Локальное", tc.mask, "")
			})
		}
	}
}
