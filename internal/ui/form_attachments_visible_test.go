package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// План 181C (#1621): actions.attachments.visible:false скрывает панель
// вложений только выбранной managed-формы; умолчание и visible:true — панель
// на месте. Attachment endpoint тестом не затрагивается.
func TestPageManagedForm_AttachmentsVisible(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }

	build := func(actions map[string]*metadata.FormAction) (*bytes.Buffer, *metadata.FormModule) {
		form := &metadata.FormModule{
			Name:       "ФормаОбъекта",
			Kind:       "object",
			EntityName: "Контрагент",
			LayoutKind: metadata.FormLayoutManaged,
			Title:      map[string]string{"ru": "Контрагент"},
			Elements: []*metadata.FormElement{{
				Kind:     metadata.FormElementField,
				Name:     "ПолеНаименование",
				TitleMap: map[string]string{"ru": "Наименование"},
				DataPath: "Объект.Наименование",
			}},
			Actions: actions,
		}
		ent := &metadata.Entity{
			Name: "Контрагент",
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
			},
			Forms: []*metadata.FormModule{form},
		}
		data := map[string]any{
			"Entity":             ent,
			"Form":               form,
			"IsNew":              false,
			"ID":                 "79f4d98a-3ce0-4da4-82ea-e9c8686e804f",
			"CanWrite":           true,
			"Values":             map[string]string{"Наименование": "Гвозди"},
			"RefOptions":         map[string]any{},
			"EnumOptions":        map[string]any{},
			"TPRefOptions":       map[string]any{},
			"FormCloseTimeoutMS": int64(31500),
			"User":               nil,
			"Lang":               "ru",
		}
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
			t.Fatalf("ExecuteTemplate: %v", err)
		}
		return &buf, form
	}

	// Умолчание: ключа нет — панель вложений на месте.
	buf, _ := build(nil)
	if !strings.Contains(buf.String(), "data-ob-attachments") {
		t.Fatal("default form misses attachments panel")
	}

	// visible:true — панель на месте.
	buf, _ = build(map[string]*metadata.FormAction{"attachments": {Visible: boolPtr(true)}})
	if !strings.Contains(buf.String(), "data-ob-attachments") {
		t.Fatal("visible:true form misses attachments panel")
	}

	// visible:false — панель скрыта, остальная форма не пострадала.
	buf, _ = build(map[string]*metadata.FormAction{"attachments": {Visible: boolPtr(false)}})
	if strings.Contains(buf.String(), "data-ob-attachments") {
		t.Fatal("visible:false form still renders attachments panel")
	}
	if !strings.Contains(buf.String(), "ПолеНаименование") {
		t.Fatal("hidden attachments broke the rest of the form")
	}
}
