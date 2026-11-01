package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Умолчание платформы — Enter как навигация (#1486). Прежнее поведение форма
// возвращает себе ключом enter_submits_form, и разметка обязана это показать:
// клиент решает по атрибуту формы, а не по догадке.
func renderManagedFormWithFlag(t *testing.T, enterSubmits bool) string {
	t.Helper()
	form := &metadata.FormModule{
		Name:             "ФормаОбъекта",
		Kind:             "object",
		EntityName:       "Контрагент",
		LayoutKind:       metadata.FormLayoutManaged,
		EnterSubmitsForm: enterSubmits,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"},
		},
	}
	ent := &metadata.Entity{
		Name:   "Контрагент",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		Forms:  []*metadata.FormModule{form},
	}
	data := map[string]any{
		"Entity":       ent,
		"Form":         form,
		"IsNew":        true,
		"Values":       map[string]string{"Наименование": ""},
		"RefOptions":   map[string]any{},
		"EnumOptions":  map[string]any{},
		"TPRefOptions": map[string]any{},
		"User":         nil,
		"Lang":         "ru",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	return buf.String()
}

func TestEnterNavigation_FlagMarksFormOnlyWhenSet(t *testing.T) {
	if html := renderManagedFormWithFlag(t, false); strings.Contains(html, "data-ob-enter-submits") {
		t.Fatal("форма без ключа помечена как отправляемая по Enter")
	}
	html := renderManagedFormWithFlag(t, true)
	if !strings.Contains(html, `data-ob-enter-submits="1"`) {
		t.Fatalf("ключ enter_submits_form не доехал до разметки")
	}
}
