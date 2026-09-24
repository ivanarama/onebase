package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// #1612: элемент с readonly_when клиент может разблокировать без перезагрузки,
// поэтому кнопка подбора и data-ob-fire-change должны быть в разметке даже
// при активном запрете. Постоянный readonly ведёт себя как раньше — «серая „…“»
// и мёртвый обработчик ему не нужны.

func roUnlockRender(t *testing.T, values map[string]string) string {
	t.Helper()
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: "Звонок", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ПолеЗаперто",
				DataPath: "Объект.Заперто", ReadOnly: true},
			{Kind: metadata.FormElementField, Name: "ПолеУсловно",
				DataPath: "Объект.Условно", ReadOnlyWhen: `Филиал = "x"`,
				Handlers: map[metadata.FormEventType]string{metadata.FormEventOnChange: "УсловноПриИзменении"}},
			{Kind: metadata.FormElementField, Name: "ПолеОбычное",
				DataPath: "Объект.Обычное"},
		},
	}
	ent := &metadata.Entity{
		Name: "Звонок", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Заперто", Type: metadata.FieldType("reference:Направления")},
			{Name: "Условно", Type: metadata.FieldType("reference:Направления")},
			{Name: "Обычное", Type: metadata.FieldType("reference:Направления")},
			{Name: "Филиал", Type: metadata.FieldType("reference:Филиалы")},
		},
		Forms: []*metadata.FormModule{form},
	}
	data := map[string]any{
		"Entity": ent, "Form": form, "IsNew": true, "CanWrite": true,
		"Values": values, "RefOptions": map[string]any{},
		"EnumOptions": map[string]any{}, "TPRefOptions": map[string]any{},
		"FormCloseTimeoutMS": int64(31500), "User": nil, "Lang": "ru",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

// elementZone — разметка элемента от его якоря до якоря следующего
// (порядок elements в форме).
func elementZone(t *testing.T, html, anchor, next string) string {
	t.Helper()
	from := strings.Index(html, `data-ob-el="`+anchor+`"`)
	if from < 0 {
		t.Fatalf("element %s not found", anchor)
	}
	to := len(html)
	if next != "" {
		if n := strings.Index(html[from+1:], `data-ob-el="`+next+`"`); n >= 0 {
			to = from + 1 + n
		}
	}
	return html[from:to]
}

func TestManagedForm_ReadonlyWhenKeepsPickerAndHandler(t *testing.T) {
	// Условие выполняется: поле заперто при отрисовке, но разметка для
	// разблокированного состояния уже на месте.
	html := roUnlockRender(t, map[string]string{"Филиал": "x", "Заперто": "", "Условно": "", "Обычное": ""})
	lockedZone := elementZone(t, html, "ПолеУсловно", "ПолеОбычное")
	if !strings.Contains(lockedZone, `data-ob-ref-picker="ref-Условно"`) {
		t.Fatalf("locked conditional ref misses picker button")
	}
	if !strings.Contains(lockedZone, `data-ob-fire-change="ПолеУсловно"`) {
		t.Fatalf("locked conditional ref misses fire-change handler trigger")
	}
	// Постоянный readonly — прежнее поведение: ни кнопки подбора, ни обработчика.
	permZone := elementZone(t, html, "ПолеЗаперто", "ПолеУсловно")
	if strings.Contains(permZone, "data-ob-ref-picker") || strings.Contains(permZone, "data-ob-fire-change") {
		t.Fatalf("permanent readonly unexpectedly got picker/handler markup")
	}
}

func TestManagedForm_ReadonlyWhenUnlockedStillCarriesMarkup(t *testing.T) {
	// Условие не выполняется: поле открыто, разметка на месте и активна.
	html := roUnlockRender(t, map[string]string{"Филиал": "", "Заперто": "", "Условно": "", "Обычное": ""})
	condZone := elementZone(t, html, "ПолеУсловно", "ПолеОбычное")
	if !strings.Contains(condZone, `data-ob-ref-picker="ref-Условно"`) {
		t.Fatalf("unlocked conditional ref misses picker button")
	}
	if !strings.Contains(condZone, `data-ob-fire-change="ПолеУсловно"`) {
		t.Fatalf("unlocked conditional ref misses fire-change")
	}
}
