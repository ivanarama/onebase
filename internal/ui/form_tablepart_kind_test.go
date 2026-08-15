package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Элемент ТЧ, описанный ключом `table_part:`, обязан отрисовываться (#830).
//
// Заявка пришла от человека, который наступил на это, собирая проверку по
// другому вопросу: таблицы на форме просто нет, а `check` и `forms validate`
// зелёные. Ключ объявлен в модели, загрузчик его читает — и на этом всё.
//
// Загрузчик теперь заполняет из него data_path, а этот тест держит конечный
// результат: таблица в HTML. Проверять только загрузчик недостаточно — дефект
// был именно в том, что модель и рендер разошлись.
func TestManagedForm_TablePartKeyRenders(t *testing.T) {
	doc := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
		},
		TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{
			{Name: "Номенклатура", Type: metadata.FieldTypeString},
			{Name: "Количество", Type: metadata.FieldTypeNumber},
		}}},
	}
	// Ровно то, что даёт загрузчик после нормализации table_part → data_path.
	form := &metadata.FormModule{
		Name:       "ФормаОбъекта",
		Kind:       "object",
		EntityName: doc.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Title:      map[string]string{"ru": "Заказ"},
		Elements: []*metadata.FormElement{{
			Kind:      metadata.FormElementTablePart,
			Name:      "Строки",
			TablePart: "Строки",
			DataPath:  "Объект.Строки",
		}},
	}
	doc.Forms = []*metadata.FormModule{form}

	data := map[string]any{
		"Entity": doc, "Form": form, "IsNew": true,
		"Values":        map[string]string{},
		"RefOptions":    map[string]any{},
		"EnumOptions":   map[string]any{},
		"ChoiceOptions": map[string]any{},
		"TPRefOptions":  map[string]any{},
		"TPEnumLabels":  map[string]map[string]map[string]string{},
		"TPEnumOrder":   map[string]map[string][]string{},
		"TPRefMeta":     map[string]any{},
		"TablePartRows": map[string][]map[string]any{"Строки": {}},
		"User":          nil,
		"Lang":          "ru",
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()

	// Колонки табличной части — признак того, что таблица действительно
	// отрисована, а не просто заголовок элемента.
	for _, want := range []string{"Номенклатура", "Количество"} {
		if !strings.Contains(html, want) {
			t.Errorf("в форме нет колонки %q — таблица не отрисована:\n%.600s", want, html)
		}
	}
}
