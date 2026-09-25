package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"golang.org/x/net/html"
)

// refAttrForm — форма с одним ссылочным реквизитом формы, показанным через
// ПолеВвода по form-local data_path (без префикса «Объект.»).
func refAttrForm(save bool) *metadata.FormModule {
	return &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: "Заявка",
		LayoutKind: metadata.FormLayoutManaged,
		Attributes: []*metadata.FormAttribute{
			{Name: "Причина", TypeRef: "CatalogRef.ПричинаОтказа", Save: save},
		},
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ПолеПричина", DataPath: "Причина"},
		},
	}
}

// renderRefAttrForm рендерит page-managed-form с заданными RefOptions.
func renderRefAttrForm(t *testing.T, form *metadata.FormModule, refOpts map[string][]map[string]any) string {
	return renderRefAttrFormState(t, form, refOpts, nil, map[string]string{"Причина": "ref-42"})
}

func renderRefAttrFormState(t *testing.T, form *metadata.FormModule, refOpts map[string][]map[string]any,
	refFilter map[string]string, values map[string]string,
) string {
	t.Helper()
	ent := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
		Forms:  []*metadata.FormModule{form},
	}
	data := map[string]any{
		"Entity":        ent,
		"Form":          form,
		"IsNew":         true,
		"Values":        values,
		"RefOptions":    refOpts,
		"RefFilter":     refFilter,
		"EnumOptions":   map[string]any{},
		"ChoiceOptions": loadChoiceOptions(form, "ru"),
		"TPRefOptions":  map[string]any{},
		"TPEnumLabels":  map[string]map[string]map[string]string{},
		"TPEnumOrder":   map[string]map[string][]string{},
		"TPRefMeta":     map[string]any{},
		"TablePartRows": map[string][]map[string]any{},
		"User":          nil,
		"Lang":          "ru",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	return buf.String()
}

// При загруженных опциях ссылочный реквизит формы получает рабочий пикер:
// select с вариантами справочника, текущее значение помечено selected.
func TestRefAttrRendersPickerWithOptions(t *testing.T) {
	html := renderRefAttrForm(t, refAttrForm(false), map[string][]map[string]any{
		"Причина": {{"id": "ref-42", "_label": "Дорого"}, {"id": "ref-7", "_label": "Нет в наличии"}},
	})
	if !strings.Contains(html, `data-ref-entity="ПричинаОтказа"`) {
		t.Error("нет пикера с data-ref-entity для ссылочного реквизита формы")
	}
	if !strings.Contains(html, "Дорого") {
		t.Error("варианты справочника не отрисовались")
	}
	if !strings.Contains(html, `value="ref-42" selected`) {
		t.Error("текущее значение не помечено selected")
	}
}

// Без загруженных опций пикер НЕ рисуется: пустой select потерял бы текущее
// значение при записи (формы обработок рендерят page-managed-form напрямую,
// mergeFormLocalRefOptions там не вызывается; то же при save:true и при
// неизвестной реестру сущности). Остаётся текстовый ввод со значением.
func TestRefAttrKeepsTextInputWithoutOptions(t *testing.T) {
	for _, tc := range []struct {
		name string
		save bool
	}{{"save=false, опции не загружены", false}, {"save=true", true}} {
		t.Run(tc.name, func(t *testing.T) {
			html := renderRefAttrForm(t, refAttrForm(tc.save), map[string][]map[string]any{})
			if strings.Contains(html, `<select id="ref-Причина"`) {
				t.Error("отрисован пикер без опций — пустой select теряет значение")
			}
			if !strings.Contains(html, `value="ref-42"`) {
				t.Error("текущее значение реквизита потеряно при рендере")
			}
		})
	}
}

func TestSaveFalseOwnerReferenceKeepsEmptyPickerContract(t *testing.T) {
	form := refAttrForm(false)
	rawFilter := `{"Владелец":{"from":"Контрагент","value":""}}`
	rendered := renderRefAttrFormState(t, form, map[string][]map[string]any{"Причина": {}},
		map[string]string{"Причина": rawFilter}, map[string]string{"Причина": "", "Контрагент": ""})
	doc, err := html.Parse(strings.NewReader(rendered))
	if err != nil {
		t.Fatal(err)
	}
	selectNode := findSelectByName(doc, "Причина")
	if selectNode == nil {
		t.Fatal("save:false owner reference with an empty owner became an inert text input")
	}
	if entity, _ := htmlAttribute(selectNode, "data-ref-entity"); entity != "ПричинаОтказа" {
		t.Fatalf("data-ref-entity = %q", entity)
	}
	if filter, _ := htmlAttribute(selectNode, "data-ref-filter"); filter != rawFilter {
		t.Fatalf("data-ref-filter = %q, want %q", filter, rawFilter)
	}
}

// mergeFormLocalRefOptions не затирает опции, уже собранные для полей сущности.
func TestMergeFormLocalRefOptionsKeepsEntityOptions(t *testing.T) {
	s := &Server{reg: runtime.NewRegistry()}
	form := &metadata.FormModule{Attributes: []*metadata.FormAttribute{
		{Name: "Причина", TypeRef: "CatalogRef.ПричинаОтказа", Save: false},
	}}
	data := map[string]any{"RefOptions": map[string][]map[string]any{
		"Контрагент": {{"id": "1", "_label": "ООО Ромашка"}},
	}}
	s.mergeFormLocalRefOptions(context.Background(), form, data)
	got, _ := data["RefOptions"].(map[string][]map[string]any)
	if len(got["Контрагент"]) != 1 {
		t.Fatalf("опции поля сущности затёрты: %+v", got)
	}
}
