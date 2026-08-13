package ui

import (
	"bytes"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Колонки SlickGrid уезжают в data-sg-cols. Чтобы подбор из ячейки ТЧ мог
// предложить «+ Создать», признак allow_inline_create должен доехать до
// клиента: без него редактор открывает форму выбора без создания (issue #765).
func TestManagedGridColumnsCarryInlineCreate(t *testing.T) {
	yes := true
	ent := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}},
		TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{
			{Name: "КлиентБезСоздания", Type: "reference:Клиент", RefEntity: "Клиент"},
			{Name: "КлиентСоСозданием", Type: "reference:Клиент", RefEntity: "Клиент", AllowInlineCreate: &yes},
			{Name: "Количество", Type: metadata.FieldTypeNumber},
		}}},
	}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementTablePart, Name: "Строки", DataPath: "Объект.Строки",
		}},
	}

	var buf bytes.Buffer
	err := tmpl.ExecuteTemplate(&buf, "page-managed-form", map[string]any{
		"Entity": ent, "Form": form, "IsNew": true, "CanWrite": true,
		"Values": map[string]string{}, "RefOptions": map[string]any{},
		"EnumOptions": map[string]any{}, "TablePartRows": map[string][]map[string]any{},
		"TPRefOptions": map[string]any{}, "TPEnumLabels": map[string]any{},
		"Lang": "ru",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	cols := parseManagedTPColumns(t, buf.String())
	byID := make(map[string]managedTPColumnJSON, len(cols))
	for _, col := range cols {
		byID[col.ID] = col
	}
	if !byID["КлиентСоСозданием"].AllowCreate {
		t.Errorf("колонка с allow_inline_create не отдала allowCreate клиенту: %+v", cols)
	}
	if byID["КлиентБезСоздания"].AllowCreate {
		t.Errorf("создание включилось у колонки без allow_inline_create (в ТЧ дефолт — выключено): %+v", cols)
	}
	if byID["Количество"].AllowCreate {
		t.Errorf("allowCreate протёк в нессылочную колонку: %+v", cols)
	}
}
