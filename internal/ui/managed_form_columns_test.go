package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Ширина группы (width) доезжает до разметки колонки. Без неё ширина колонки
// равна max-content содержимого: одно широкое поле забирает всю строку, и
// соседняя колонка — обычно та, где кнопки, — переносится под форму.
func TestManagedFormGroupWidthRendersAsFlexBasis(t *testing.T) {
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: "Обращение",
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementGroupBox, Name: "ГруппаРабочаяОбласть", Orientation: "horizontal",
			Children: []*metadata.FormElement{
				{Kind: metadata.FormElementGroupBox, Name: "Левая", Width: 470,
					Children: []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "ПолеНомер", DataPath: "Объект.Номер"}}},
				{Kind: metadata.FormElementGroupBox, Name: "Правая",
					Children: []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "ПолеТип", DataPath: "Объект.Тип"}}},
			},
		}},
	}
	entity := &metadata.Entity{
		Name: "Обращение", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}, {Name: "Тип", Type: metadata.FieldTypeString}},
		Forms:  []*metadata.FormModule{form},
	}
	html := renderManagedForm(t, entity, form, map[string]string{})
	if !strings.Contains(html, "flex:0 1 470px;min-width:0") {
		t.Error("ширина группы не доехала до стиля колонки")
	}
	// Колонка без width растягивается сама — иначе справа остаётся пустое место.
	if !strings.Contains(html, ".managed-group-horizontal>.managed-group-body>.form-group-box{min-width:0;flex:1 1 auto}") {
		t.Error("колонки формы не делят ширину строки")
	}
	// Карточка управляемой формы занимает всю ширину рабочей области.
	if !strings.Contains(html, "main>.card{max-width:none}") {
		t.Error("карточка управляемой формы осталась ограниченной по ширине")
	}
}

func renderManagedForm(t *testing.T, entity *metadata.Entity, form *metadata.FormModule, values map[string]string) string {
	t.Helper()
	data := map[string]any{
		"Entity": entity, "Form": form, "IsNew": true,
		"Values":        values,
		"RefOptions":    map[string][]map[string]any{},
		"EnumOptions":   map[string]any{},
		"ChoiceOptions": loadChoiceOptions(form, "ru"),
		"TPRefOptions":  map[string]any{},
		"TPEnumLabels":  map[string]map[string]map[string]string{},
		"TPEnumOrder":   map[string]map[string][]string{},
		"TPRefMeta":     map[string]any{},
		"TablePartRows": map[string][]map[string]any{},
		"User":          nil, "Lang": "ru",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	return buf.String()
}
