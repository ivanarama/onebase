package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// ref_card_button: false выключает кнопку «Открыть карточку» (🔍) у ссылочных
// полей формы. Умолчание — показывать: формы, которые про ключ не высказались
// (и формы обработок, куда признак вообще не кладётся), должны рисовать её
// по-прежнему.
func TestManagedRefCardButton(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{{
			Name:      "Клиент",
			Type:      metadata.FieldType("reference:Клиент"),
			RefEntity: "Клиент",
		}},
	}
	el := &metadata.FormElement{
		Kind:     metadata.FormElementField,
		Name:     "ПолеКлиент",
		DataPath: "Объект.Клиент",
	}
	render := func(ctx map[string]any) string {
		t.Helper()
		ctx["Entity"] = ent
		ctx["Values"] = map[string]string{"Клиент": "cl-1"}
		ctx["RefOptions"] = map[string]any{"Клиент": []map[string]any{{"id": "cl-1", "_label": "ООО Ромашка"}}}
		ctx["EnumOptions"] = map[string]any{}
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "managed-element", map[string]any{"El": el, "Ctx": ctx}); err != nil {
			t.Fatalf("execute managed-element: %v", err)
		}
		return buf.String()
	}

	// Ключа нет — поведение прежнее.
	if html := render(map[string]any{}); !strings.Contains(html, `data-ob-ref-current="ref-Клиент"`) {
		t.Errorf("без ref_card_button кнопка «Открыть карточку» должна остаться:\n%s", html)
	}
	// Форма выключила кнопку.
	if html := render(map[string]any{"HideRefCard": true}); strings.Contains(html, `data-ob-ref-current="ref-Клиент"`) {
		t.Errorf("при ref_card_button: false кнопка «Открыть карточку» рисоваться не должна:\n%s", html)
	}
}
