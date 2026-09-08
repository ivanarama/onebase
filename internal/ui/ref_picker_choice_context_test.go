package ui

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/ivantit66/onebase/internal/metadata"
)

// choice_context хранит пути, а не снимок значений серверного рендера. Поэтому
// смена Филиала без перерендера формы попадёт в следующий запрос подбора.
func TestRefPickerChoiceContextReadsCurrentControl(t *testing.T) {
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: "Заявка",
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ПолеФилиал", DataPath: "Объект.Филиал"},
			{
				Kind: metadata.FormElementField, Name: "ПолеНаправление", DataPath: "Объект.Направление",
				ChoiceContext: map[string]string{"Филиал": "Объект.Филиал"},
			},
		},
	}
	ent := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindCatalog, Forms: []*metadata.FormModule{form},
		Fields: []metadata.Field{
			{Name: "Филиал", Type: metadata.FieldTypeString},
			{Name: "Направление", Type: metadata.FieldType("reference:Направление"), RefEntity: "Направление"},
		},
	}
	data := map[string]any{
		"Entity": ent, "Form": form, "IsNew": true,
		"Values":      map[string]string{"Филиал": "old-branch", "Направление": ""},
		"RefOptions":  map[string][]map[string]any{"Направление": {}},
		"EnumOptions": map[string]any{}, "ChoiceOptions": map[string]any{},
		"TPRefOptions": map[string]any{}, "TPEnumLabels": map[string]any{},
		"TPEnumOrder": map[string]any{}, "TPRefMeta": map[string]any{},
		"TablePartRows": map[string][]map[string]any{}, "Lang": "ru",
	}
	var rendered bytes.Buffer
	if err := tmpl.ExecuteTemplate(&rendered, "page-managed-form", data); err != nil {
		t.Fatal(err)
	}
	doc, err := html.Parse(strings.NewReader(rendered.String()))
	if err != nil {
		t.Fatal(err)
	}
	var rawContext string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "select" {
			id := htmlAttribute(node, "id")
			if id == "ref-Направление" {
				rawContext = htmlAttribute(node, "data-ref-context")
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	var paths map[string]string
	if err := json.Unmarshal([]byte(rawContext), &paths); err != nil {
		t.Fatalf("data-ref-context = %q: %v", rawContext, err)
	}
	if got := paths["Филиал"]; got != "Объект.Филиал" {
		t.Fatalf("choice_context сохранил %q вместо пути Объект.Филиал", got)
	}
	if strings.Contains(rawContext, "old-branch") {
		t.Fatal("choice_context содержит снимок значения серверного рендера")
	}

	js := string(uiJS)
	start := strings.Index(js, "function refContextForRequest(sel)")
	end := strings.Index(js, "function openRefCurrent(selOrId)")
	if start < 0 || end <= start {
		t.Fatal("не найден runtime формы выбора")
	}
	picker := js[start:end]
	for _, want := range []string{
		"sel.form.elements.namedItem(fieldName)",
		"var refContext = refContextForRequest(sel);",
	} {
		if !strings.Contains(picker, want) {
			t.Fatalf("runtime не читает текущее значение при каждом запросе: нет %q", want)
		}
	}
}

func htmlAttribute(node *html.Node, name string) string {
	for _, attribute := range node.Attr {
		if attribute.Key == name {
			return attribute.Val
		}
	}
	return ""
}

func TestOnlyReferencePickerWaitsForPreviewLayout(t *testing.T) {
	js := string(uiJS)
	itemStart := strings.Index(js, "function openItemPicker(")
	refStart := strings.Index(js, "function refContextForRequest(sel)")
	refEnd := strings.Index(js, "function openRefCurrent(selOrId)")
	if itemStart < 0 || refStart <= itemStart || refEnd <= refStart {
		t.Fatal("не найдены функции picker в ui.js")
	}
	if strings.Contains(js[itemStart:refStart], "visibility:hidden") {
		t.Fatal("openItemPicker остаётся навсегда скрытым")
	}
	refPicker := js[refStart:refEnd]
	if !strings.Contains(refPicker, "visibility:hidden") || !strings.Contains(refPicker, "rpReveal()") {
		t.Fatal("reference picker должен быть скрыт до вычисления layout и затем показан")
	}
}
