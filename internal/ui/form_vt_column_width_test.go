package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// width колонки таблицы значений формы уходит в заголовок простой таблицы —
// как у колонки табличной части; колонка без width стиля не получает.
func TestManagedFormValueTableColumnWidth(t *testing.T) {
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementTablePart, Name: "ЭлементКомментарии", DataPath: "Форма.Комментарии", NoGrid: true,
			Children: []*metadata.FormElement{
				{Kind: metadata.FormElementColumn, Name: "КолДата", DataPath: "Форма.Комментарии.Дата", Width: 130},
				{Kind: metadata.FormElementColumn, Name: "КолТекст", DataPath: "Форма.Комментарии.Текст"},
			},
		}},
		Attributes: []*metadata.FormAttribute{{
			Name: "Комментарии", TypeRef: "ValueTable",
			Columns: []*metadata.FormAttributeColumn{{Name: "Дата", TypeRef: "string"}, {Name: "Текст", TypeRef: "string"}},
		}},
	}
	ent := &metadata.Entity{Name: "Заказ", Kind: metadata.KindDocument, Forms: []*metadata.FormModule{form}}
	data := map[string]any{
		"Entity": ent, "Form": form, "IsNew": true, "Values": map[string]string{},
		"RefOptions": map[string]any{}, "EnumOptions": map[string]any{}, "ChoiceOptions": map[string]any{},
		"TPRefOptions": map[string]any{}, "TPEnumLabels": map[string]map[string]map[string]string{},
		"TPEnumOrder": map[string]map[string][]string{}, "TPRefMeta": map[string]any{},
		"TablePartRows": map[string][]map[string]any{"Комментарии": {{"Дата": "16.10.2026 10:00", "Текст": "т"}}},
		"Lang":          "ru",
	}
	var rendered bytes.Buffer
	if err := tmpl.ExecuteTemplate(&rendered, "page-managed-form", data); err != nil {
		t.Fatal(err)
	}
	html := rendered.String()
	if !strings.Contains(html, `<th style="width:130px">Дата</th>`) {
		t.Errorf("ширина колонки таблицы значений не дошла до заголовка: %s", html)
	}
	if !strings.Contains(html, `<th>Текст</th>`) {
		t.Errorf("колонка без width получила стиль: %s", html)
	}
}
