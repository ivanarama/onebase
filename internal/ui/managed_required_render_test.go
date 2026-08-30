package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"golang.org/x/net/html"
)

func TestManagedRequiredRenderer(t *testing.T) {
	tests := []struct {
		name           string
		field          metadata.Field
		element        *metadata.FormElement
		numerator      *metadata.Numerator
		wantControls   int
		wantRequired   bool
		wantRequiredUI bool
	}{
		{
			name:  "form-only text",
			field: metadata.Field{Name: "Наименование", Type: metadata.FieldTypeString},
			element: &metadata.FormElement{
				Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование", Required: true,
			},
			wantControls: 1, wantRequired: true, wantRequiredUI: true,
		},
		{
			name:         "metadata auto-number is indicated but not browser-blocked",
			field:        metadata.Field{Name: metadata.StandardCodeField, Type: metadata.FieldTypeString, Required: true},
			element:      fieldEl("ПолеКод", "Объект."+metadata.StandardCodeField),
			numerator:    &metadata.Numerator{Prefix: "К-", Length: 6},
			wantControls: 1, wantRequiredUI: true,
		},
		{
			name: "metadata-only reference",
			field: metadata.Field{
				Name: "Контрагент", Type: metadata.FieldType("reference:Контрагенты"), RefEntity: "Контрагенты", Required: true,
			},
			element: &metadata.FormElement{
				Kind: metadata.FormElementField, Name: "Контрагент", DataPath: "Объект.Контрагент",
			},
			wantControls: 1, wantRequired: true, wantRequiredUI: true,
		},
		{
			name:  "date picker",
			field: metadata.Field{Name: "Дата", Type: metadata.FieldTypeDate},
			element: &metadata.FormElement{
				Kind: metadata.FormElementDatePicker, Name: "Дата", DataPath: "Объект.Дата", Required: true,
			},
			wantControls: 1, wantRequired: true, wantRequiredUI: true,
		},
		{
			name:  "metadata-only list",
			field: metadata.Field{Name: "Статус", Type: metadata.FieldTypeString, Required: true},
			element: &metadata.FormElement{
				Kind: metadata.FormElementInputList, Name: "Статус", DataPath: "Объект.Статус",
				Choices: []metadata.FormChoice{{Value: "новый", Title: map[string]string{"ru": "Новый"}}},
			},
			wantControls: 1, wantRequired: true, wantRequiredUI: true,
		},
		{
			name:  "switch select",
			field: metadata.Field{Name: "Приоритет", Type: metadata.FieldTypeString},
			element: &metadata.FormElement{
				Kind: metadata.FormElementSwitch, Name: "Приоритет", DataPath: "Объект.Приоритет", Required: true, View: "select",
				Options: []metadata.FormOption{{Value: "обычный", Labels: map[string]string{"ru": "Обычный"}}},
			},
			wantControls: 1, wantRequired: true, wantRequiredUI: true,
		},
		{
			name:  "switch radio group",
			field: metadata.Field{Name: "Состояние", Type: metadata.FieldTypeString},
			element: &metadata.FormElement{
				Kind: metadata.FormElementSwitch, Name: "Состояние", DataPath: "Объект.Состояние", Required: true,
				Options: []metadata.FormOption{
					{Value: "новый", Labels: map[string]string{"ru": "Новый"}},
					{Value: "готов", Labels: map[string]string{"ru": "Готов"}},
				},
			},
			wantControls: 2, wantRequired: true, wantRequiredUI: true,
		},
		{
			name:  "optional text",
			field: metadata.Field{Name: "Комментарий", Type: metadata.FieldTypeString},
			element: &metadata.FormElement{
				Kind: metadata.FormElementField, Name: "Комментарий", DataPath: "Объект.Комментарий",
			},
			wantControls: 1,
		},
		{
			name:  "metadata requirement does not leak to form attribute",
			field: metadata.Field{Name: "Комментарий", Type: metadata.FieldTypeString, Required: true},
			element: &metadata.FormElement{
				Kind: metadata.FormElementField, Name: "КомментарийФормы", DataPath: "Форма.Комментарий",
			},
			wantControls: 1,
		},
		{
			name:  "readonly required text",
			field: metadata.Field{Name: "Код", Type: metadata.FieldTypeString, Required: true},
			element: &metadata.FormElement{
				Kind: metadata.FormElementField, Name: "Код", DataPath: "Объект.Код", ReadOnly: true,
			},
			wantControls: 1, wantRequiredUI: true,
		},
		{
			name:  "required checkbox accepts false",
			field: metadata.Field{Name: "Активен", Type: metadata.FieldTypeBool, Required: true},
			element: &metadata.FormElement{
				Kind: metadata.FormElementCheckbox, Name: "Активен", DataPath: "Объект.Активен",
			},
			wantControls: 1, wantRequiredUI: true,
		},
		{
			name:  "image keeps hidden backing input",
			field: metadata.Field{Name: "Фото", Type: metadata.FieldTypeImage, Required: true},
			element: &metadata.FormElement{
				Kind: metadata.FormElementField, Name: "Фото", DataPath: "Объект.Фото",
			},
			wantControls: 1, wantRequiredUI: true,
		},
		{
			name:  "code editor relies on server validation",
			field: metadata.Field{Name: "Сценарий", Type: metadata.FieldTypeString, Required: true},
			element: &metadata.FormElement{
				Kind: metadata.FormElementCodeField, Name: "Сценарий", DataPath: "Объект.Сценарий",
			},
			wantControls: 1, wantRequiredUI: true,
		},
		{
			name:  "rich text editor relies on server validation",
			field: metadata.Field{Name: "Описание", Type: metadata.FieldTypeRichText, Required: true},
			element: &metadata.FormElement{
				Kind: metadata.FormElementField, Name: "Описание", DataPath: "Объект.Описание",
			},
			wantControls: 1, wantRequiredUI: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rendered := renderManagedRequiredElement(t, tt.field, tt.element, tt.numerator)
			root, err := html.Parse(strings.NewReader(rendered))
			if err != nil {
				t.Fatalf("parse rendered element: %v", err)
			}

			controls := requiredNamedControls(root, tt.field.Name)
			if len(controls) != tt.wantControls {
				t.Fatalf("named controls = %d, want %d; HTML:\n%s", len(controls), tt.wantControls, rendered)
			}
			for _, control := range controls {
				_, gotRequired := requiredHTMLAttr(control, "required")
				if gotRequired != tt.wantRequired {
					t.Errorf("<%s name=%q> required = %v, want %v; HTML:\n%s", control.Data, tt.field.Name, gotRequired, tt.wantRequired, rendered)
				}
			}
			if got := requiredMarkerCount(root) > 0; got != tt.wantRequiredUI {
				t.Errorf("required marker = %v, want %v; HTML:\n%s", got, tt.wantRequiredUI, rendered)
			}
		})
	}
}

func renderManagedRequiredElement(t *testing.T, field metadata.Field, element *metadata.FormElement, numerator *metadata.Numerator) string {
	t.Helper()
	entity := &metadata.Entity{Name: "Тест", Kind: metadata.KindCatalog, Fields: []metadata.Field{field}, Numerator: numerator}
	form := &metadata.FormModule{LayoutKind: metadata.FormLayoutManaged, Elements: []*metadata.FormElement{element}}
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
	if err := tmpl.ExecuteTemplate(&rendered, "managed-element", map[string]any{"El": element, "Ctx": ctx}); err != nil {
		t.Fatalf("render managed element: %v", err)
	}
	return rendered.String()
}

func requiredNamedControls(root *html.Node, name string) []*html.Node {
	var controls []*html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && (node.Data == "input" || node.Data == "select" || node.Data == "textarea") {
			if value, ok := requiredHTMLAttr(node, "name"); ok && value == name {
				controls = append(controls, node)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return controls
}

func requiredMarkerCount(root *html.Node) int {
	count := 0
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "span" && strings.TrimSpace(requiredNodeText(node)) == "*" {
			count++
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return count
}

func requiredNodeText(root *html.Node) string {
	var text strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			text.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return text.String()
}

func requiredHTMLAttr(node *html.Node, name string) (string, bool) {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val, true
		}
	}
	return "", false
}
