package ui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
	"golang.org/x/net/html"
)

// hint по умолчанию — всплывающая подсказка (значок ⓘ у подписи), а не текст
// под полем: у списка «Да/Нет», флажка и остальных полей одинаково. Текст под
// полем — только с hint_inline. Карточка открыта штатным обработчиком формы.
func TestManagedFormHintIsTooltipByDefault(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ОГД", Type: metadata.FieldTypeBool},
			{Name: "Звёздочка", Type: metadata.FieldTypeBool},
			{Name: "Примечание", Type: metadata.FieldTypeString},
		},
	}
	ent.Forms = []*metadata.FormModule{managedObjectForm(
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ПолеОГД", DataPath: "Объект.ОГД", Hint: "Особо гарантийный документ"},
		&metadata.FormElement{Kind: "Флажок", Name: "ПолеЗвёздочка", DataPath: "Объект.Звёздочка", Hint: "Клиент с особыми условиями"},
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ПолеПримечание", DataPath: "Объект.Примечание", Hint: "видно под полем", HintInline: true},
	)}
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
	req := reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/new", nil, map[string]string{"entity": ent.Name})
	rec := httptest.NewRecorder()
	srv.form(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("form: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<span class="ob-hint" title="Особо гарантийный документ" aria-label="Особо гарантийный документ">ⓘ</span></label>`,
		`<span class="ob-hint" title="Клиент с особыми условиями" aria-label="Клиент с особыми условиями">ⓘ</span></label>`,
		`<small style="color:#94a3b8;font-size:11px">видно под полем</small>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("нет %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `<small style="color:#94a3b8;font-size:11px">Особо гарантийный документ</small>`) {
		t.Error("hint списка «Да/Нет» напечатан текстом под полем, а должен всплывать")
	}
	if strings.Contains(body, `title="видно под полем" aria-label`) {
		t.Error("с hint_inline значок ⓘ не нужен — подсказка уже видна текстом")
	}
}

func TestManagedFormHintInlineSpecialFieldsHTTP(t *testing.T) {
	for _, field := range []struct {
		kind string
		typ  metadata.FieldType
	}{
		{"Флажок", metadata.FieldTypeBool},
		{"ПолеДаты", metadata.FieldTypeDate},
		{"ПолеКода", metadata.FieldTypeString},
	} {
		for _, mode := range []struct {
			name   string
			yaml   string
			inline bool
			empty  bool
		}{
			{name: "default"},
			{name: "tooltip", yaml: "    hint_inline: false\n"},
			{name: "inline", yaml: "    hint_inline: true\n", inline: true},
			{name: "empty", yaml: "    hint_inline: true\n", inline: true, empty: true},
		} {
			t.Run(field.kind+"/"+mode.name, func(t *testing.T) {
				hint := `Подсказка <script> & "текст"`
				if mode.empty {
					hint = ""
				}
				path := filepath.Join(t.TempDir(), "форма.form.yaml")
				source := fmt.Sprintf(`schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заявка
elements:
  - kind: %s
    name: ПолеПодсказки
    data_path: Объект.Значение
    hint: '%s'
%s`, field.kind, hint, mode.yaml)
				if err := os.WriteFile(path, []byte(source), 0600); err != nil {
					t.Fatal(err)
				}
				form, err := loader.NewManagedFormLoader().LoadFormFile(path, "Заявка")
				if err != nil {
					t.Fatal(err)
				}
				ent := &metadata.Entity{
					Name: "Заявка", Kind: metadata.KindCatalog,
					Fields: []metadata.Field{{Name: "Значение", Type: field.typ}},
					Forms:  []*metadata.FormModule{form},
				}
				srv, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
				req := reqWithChi(http.MethodGet, "/ui/catalog/Заявка/new", nil, map[string]string{"entity": ent.Name})
				rec := httptest.NewRecorder()
				srv.form(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("form: %d %s", rec.Code, rec.Body.String())
				}
				wrapper := managedElementAnchor(t, rec.Body.String(), "ПолеПодсказки")
				var texts []string
				var tooltips int
				var walk func(*html.Node)
				walk = func(n *html.Node) {
					if n.Type == html.ElementNode {
						if n.Data == "small" && n.FirstChild != nil {
							texts = append(texts, n.FirstChild.Data)
						}
						if class, _ := managedHTMLAttr(n, "class"); class == "ob-hint" {
							tooltips++
							for _, key := range []string{"title", "aria-label"} {
								if value, _ := managedHTMLAttr(n, key); value != hint {
									t.Errorf("tooltip %s=%q, want %q", key, value, hint)
								}
							}
						}
					}
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						walk(c)
					}
				}
				walk(wrapper)
				if mode.inline && !mode.empty {
					if len(texts) != 1 || texts[0] != hint || tooltips != 0 {
						t.Errorf("inline hint: texts=%q tooltips=%d", texts, tooltips)
					}
				} else {
					wantTooltips := 1
					if mode.empty {
						wantTooltips = 0
					}
					if len(texts) != 0 || tooltips != wantTooltips {
						t.Errorf("hint: texts=%q tooltips=%d, want %d tooltips", texts, tooltips, wantTooltips)
					}
				}
				if strings.Contains(rec.Body.String(), "<script> &") {
					t.Error("hint must be escaped as text")
				}
			})
		}
	}
}
