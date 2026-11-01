package cli

import (
	"encoding/json"
	"testing"
)

// Кнопка «Открыть карточку» (#1876): `onebase schema form` публикует оба ключа
// формы и ключ поля булевыми — по ним редакторы и агенты пишут YAML формы.
func TestSchemaFormPublishesRefCardButtonKeys(t *testing.T) {
	out, err := captureStdout(t, func() error {
		_, err := executeRootArgs(t, "schema", "form")
		return err
	})
	if err != nil {
		t.Fatalf("onebase schema form: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("schema form returned invalid JSON: %v\n%s", err, out)
	}
	form := schemaAt(t, doc, "properties", "form", "properties")
	for _, key := range []string{"ref_card_button", "ref_card_button_admin_only"} {
		if schemaAt(t, form, key)["type"] != "boolean" {
			t.Errorf("form.%s не булев: %#v", key, form[key])
		}
	}
	element := schemaAt(t, doc, "properties", "elements", "items", "properties")
	if schemaAt(t, element, "ref_card_button")["type"] != "boolean" {
		t.Errorf("elements[].ref_card_button не булев: %#v", element["ref_card_button"])
	}
}
