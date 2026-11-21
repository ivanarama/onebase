package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestPageForm_DelegatedHandlers(t *testing.T) {
	for _, old := range []string{"onclick=", "onchange=", "oninput=", "onsubmit=", "javascript:void"} {
		if strings.Contains(tplForm, old) {
			t.Fatalf("tplForm содержит inline handler/JS URL %q", old)
		}
	}
	for _, want := range []string{
		"data-ob-popup-cancel",
		"data-ob-close-tab",
		"data-ob-toggle-next",
		"data-ob-confirm",
		"data-ob-ref-current",
		"data-ob-image-upload",
		"data-ob-image-clear",
		"data-ob-ref-picker-self",
		"data-ob-ref-current-self",
		"data-ob-tp-recalc",
		"data-ob-remove-row",
		"data-ob-add-tp-row",
		"data-ob-submit-form",
		"data-ob-file-click",
	} {
		if !strings.Contains(tplForm, want) {
			t.Fatalf("tplForm не содержит delegated marker %q", want)
		}
	}

	ent := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Контрагент", Type: metadata.FieldType("reference:Контрагент"), RefEntity: "Контрагент"},
			{Name: "Картинка", Type: metadata.FieldTypeImage},
		},
		TableParts: []metadata.TablePart{
			{
				Name: "Товары",
				Fields: []metadata.Field{
					{Name: "Номенклатура", Type: metadata.FieldType("reference:Номенклатура"), RefEntity: "Номенклатура"},
					{Name: "Количество", Type: metadata.FieldTypeNumber},
					{Name: "Комментарий", Type: metadata.FieldTypeString},
				},
			},
		},
	}
	data := map[string]any{
		"Entity":    ent,
		"ID":        "11111111-1111-1111-1111-111111111111",
		"IsNew":     false,
		"IsPopup":   false,
		"CanWrite":  true,
		"CanDelete": true,
		"AllPrintForms": []map[string]any{
			{"Name": "Акт", "External": false},
		},
		"Values": map[string]string{
			"Контрагент": "c1",
			"Картинка":   "img-1",
		},
		"RefOptions": map[string]any{
			"Контрагент": []map[string]any{{"id": "c1", "_label": "Ромашка"}},
		},
		"EnumOptions": map[string]any{},
		"TPRefOptions": map[string]any{
			"Товары": map[string]any{
				"Номенклатура": []map[string]any{{"id": "n1", "_label": "Товар"}},
			},
		},
		"TPRefMeta": map[string]any{
			"Товары": map[string]any{
				"Номенклатура": map[string]any{"entity": "Номенклатура"},
			},
		},
		"TablePartRows": map[string]any{
			"Товары": []map[string]any{{"Номенклатура": "n1", "Количество": "2", "Комментарий": "строка"}},
		},
		"Lang":           "ru",
		"LegacyEntityUI": true,
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-form", data); err != nil {
		t.Fatalf("ExecuteTemplate page-form: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		`data-ob-close-tab`,
		`data-ob-toggle-next`,
		`data-ob-confirm=`,
		`data-ob-ref-picker="ref-Контрагент"`,
		`data-ob-ref-current="ref-Контрагент"`,
		`data-ob-image-upload="/ui/document/заказ/_image"`,
		`data-ob-image-clear`,
		`data-ob-ref-picker-self`,
		`data-ob-ref-current-self`,
		`data-ob-tp-recalc`,
		`data-ob-remove-row="tr"`,
		`data-ob-add-tp-row`,
		`data-tp-fields="Номенклатура,Количество,Комментарий"`,
		`data-tp-num-fields="Количество"`,
		`data-ob-submit-form="att-upload-form"`,
		`data-ob-file-click="att-file-input"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page-form не содержит %q", want)
		}
	}
}
