package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Строковый реквизит с multiline редактируется многострочным полем и в
// АВТОФОРМЕ карточки: до этого признак был только у элемента управляемой формы,
// и памятку в справочнике приходилось править однострочным вводом.
func TestAutoFormRendersMultilineFieldAsTextarea(t *testing.T) {
	ent := &metadata.Entity{
		Name: "НаправлениеОбслуживания", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Информация", Type: metadata.FieldTypeString, Multiline: true},
		},
	}
	data := map[string]any{
		"Entity": ent, "IsNew": true,
		"Values":        map[string]string{"Наименование": "Ремонт", "Информация": "НЕ ВЫПОЛНЯЕМ: промышленные машины"},
		"RefOptions":    map[string][]map[string]any{},
		"EnumOptions":   map[string]any{},
		"FolderOptions": []map[string]any{},
		"TPRefOptions":  map[string]any{},
		"TPEnumLabels":  map[string]map[string]map[string]string{},
		"TPEnumOrder":   map[string]map[string][]string{},
		"TPRefMeta":     map[string]any{},
		"TablePartRows": map[string][]map[string]any{},
		"User":          nil, "Lang": "ru",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, `<textarea name="Информация"`) {
		t.Error("многострочный реквизит в карточке отрисован не как textarea")
	}
	if !strings.Contains(html, "НЕ ВЫПОЛНЯЕМ: промышленные машины</textarea>") {
		t.Error("значение многострочного реквизита не попало в textarea")
	}
	if !strings.Contains(html, `<input type="text" autocomplete="off" name="Наименование"`) {
		t.Error("обычный строковый реквизит перестал быть однострочным вводом")
	}
}

// То же для формы записи независимого регистра сведений: памятка по паре
// «филиал + направление» правится там, и однострочный ввод её не показывает.
func TestInfoRegFormRendersMultilineResourceAsTextarea(t *testing.T) {
	ir := &metadata.InfoRegister{
		Name: "ПримечанияПоНаправлениям",
		Dimensions: []metadata.Field{
			{Name: "Филиал", Type: metadata.FieldType("reference:Филиал"), RefEntity: "Филиал"},
		},
		Resources: []metadata.Field{
			{Name: "Примечание", Type: metadata.FieldTypeString, Multiline: true},
			{Name: "ИдЛегаси", Type: metadata.FieldTypeString},
		},
	}
	data := map[string]any{
		"InfoReg": ir,
		"Values":  map[string]string{"Примечание": "По АВИТО не дальше 20 км от МКАД", "ИдЛегаси": ""},
		"RefOpts": map[string][]map[string]any{},
		"User":    nil, "Lang": "ru",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-inforeg-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, `<textarea name="Примечание"`) {
		t.Error("многострочный ресурс регистра отрисован не как textarea")
	}
	if !strings.Contains(html, "По АВИТО не дальше 20 км от МКАД</textarea>") {
		t.Error("значение ресурса не попало в textarea")
	}
	if !strings.Contains(html, `<input type="text" name="ИдЛегаси"`) {
		t.Error("обычный ресурс перестал быть однострочным вводом")
	}
}
