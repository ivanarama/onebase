package ui

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"golang.org/x/net/html"
)

// TestManagedRequiredInvalidHandlerRevealsHiddenTabs исполняет настоящий
// обработчик `invalid` из managed.js на структуре, отрендеренной настоящим
// шаблоном управляемой формы. Проверка присутствием строк в исходнике здесь
// бесполезна: она краснеет от переформатирования и остаётся зелёной при
// перепутанном направлении обхода вкладок — а именно оно и решает, увидит
// пользователь незаполненное поле или нет (переключение внешней группы прячет
// вложенные страницы, поэтому идти надо от внешней к внутренней).
//
// DOM для node строится из разметки шаблона, а не выдумывается в JS: иначе
// переименование класса вкладки оставило бы тест зелёным на несуществующей
// странице.
func TestManagedRequiredInvalidHandlerRevealsHiddenTabs(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the required-field tab reveal regression test")
	}

	domPath := filepath.Join(t.TempDir(), "managed-required-tabs.json")
	if err := os.WriteFile(domPath, managedRequiredTabsDOM(t), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(node, "--test", "static/managed_required_tabs_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Env = append(os.Environ(), "ONEBASE_REQUIRED_TABS_DOM="+domPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node required-tab reveal behavior test: %v\n%s", err, output)
	}
}

// managedRequiredTabsDOM рендерит управляемую форму с вложенными наборами
// вкладок и возвращает её дерево элементов в JSON — ровно то, по чему node
// собирает свой DOM.
//
// Раскладка подобрана так, чтобы обход вкладок был наблюдаем: обязательное
// «ПолеВложенное» лежит на второй странице вложенного набора внутри второй
// страницы внешнего, то есть скрыто двумя уровнями display:none.
func managedRequiredTabsDOM(t *testing.T) []byte {
	t.Helper()

	required := func(name string) *metadata.FormElement {
		return &metadata.FormElement{
			Kind: metadata.FormElementField, Name: name, DataPath: "Объект." + name, Required: true,
		}
	}
	inner := &metadata.FormElement{
		Kind: metadata.FormElementPages, Name: "ВложенныеСтраницы",
		Children: []*metadata.FormElement{
			{Kind: metadata.FormElementPage, Name: "ВложеннаяПервая", Children: []*metadata.FormElement{fieldEl("Заметка", "Объект.Заметка")}},
			{Kind: metadata.FormElementPage, Name: "ВложеннаяВторая", Children: []*metadata.FormElement{required("ПолеВложенное")}},
		},
	}
	pages := &metadata.FormElement{
		Kind: metadata.FormElementPages, Name: "Страницы",
		Children: []*metadata.FormElement{
			{Kind: metadata.FormElementPage, Name: "Основная", Children: []*metadata.FormElement{required("ПолеВкладки")}},
			{Kind: metadata.FormElementPage, Name: "Дополнительно", Children: []*metadata.FormElement{inner}},
		},
	}
	outside := required("ПолеСнаружи")

	entity := &metadata.Entity{
		Name: "Тест", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "ПолеСнаружи", Type: metadata.FieldTypeString},
			{Name: "ПолеВкладки", Type: metadata.FieldTypeString},
			{Name: "ПолеВложенное", Type: metadata.FieldTypeString},
			{Name: "Заметка", Type: metadata.FieldTypeString},
		},
	}
	form := &metadata.FormModule{LayoutKind: metadata.FormLayoutManaged, Elements: []*metadata.FormElement{outside, pages}}
	ctx := map[string]any{
		"Entity":        entity,
		"Form":          form,
		"Values":        map[string]string{},
		"RefOptions":    map[string]any{},
		"EnumOptions":   map[string]any{},
		"ChoiceOptions": loadChoiceOptions(form, "ru"),
		"Lang":          "ru",
	}

	var rendered bytes.Buffer
	for _, el := range form.Elements {
		if err := tmpl.ExecuteTemplate(&rendered, "managed-element", map[string]any{"El": el, "Ctx": ctx}); err != nil {
			t.Fatalf("render managed element %s: %v", el.Name, err)
		}
	}

	root, err := html.Parse(strings.NewReader(rendered.String()))
	if err != nil {
		t.Fatalf("parse rendered form: %v", err)
	}
	body := findHTMLElement(root, "body")
	if body == nil {
		t.Fatalf("rendered form has no body; HTML:\n%s", rendered.String())
	}

	tree := domElementNode(body)
	encoded, err := json.Marshal(tree)
	if err != nil {
		t.Fatalf("encode DOM: %v", err)
	}
	return encoded
}

// domTestNode — элемент DOM в том виде, в каком его читает node-обвязка.
// Текстовые узлы обработчику не нужны, поэтому не переносятся.
type domTestNode struct {
	Tag      string            `json:"tag"`
	Attrs    map[string]string `json:"attrs,omitempty"`
	Children []*domTestNode    `json:"children,omitempty"`
}

func domElementNode(node *html.Node) *domTestNode {
	out := &domTestNode{Tag: node.Data}
	if len(node.Attr) > 0 {
		out.Attrs = make(map[string]string, len(node.Attr))
		for _, attr := range node.Attr {
			out.Attrs[attr.Key] = attr.Val
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.ElementNode {
			continue
		}
		out.Children = append(out.Children, domElementNode(child))
	}
	return out
}

func findHTMLElement(root *html.Node, tag string) *html.Node {
	if root.Type == html.ElementNode && root.Data == tag {
		return root
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if found := findHTMLElement(child, tag); found != nil {
			return found
		}
	}
	return nil
}
