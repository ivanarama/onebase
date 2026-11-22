package cli

import (
	"encoding/json"
	"testing"
)

// Редактор получает actions.copy.visible через опубликованную команду,
// а поведение false/true/отсутствия проверяется HTTP-регрессией формы.
func TestSchemaFormPublishesCopyVisible(t *testing.T) {
	out, err := captureStdout(t, func() error {
		_, err := executeRootArgs(t, "schema", "form")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	actions := schemaAt(t, doc, "properties", "actions")
	copy := schemaAt(t, actions, "properties", "copy")
	if actions["additionalProperties"] != true {
		t.Fatal("другие стандартные действия формы должны оставаться допустимыми")
	}
	visible := schemaAt(t, copy, "properties", "visible")
	if visible["type"] != "boolean" || visible["default"] == false {
		t.Fatalf("actions.copy.visible должен быть булевым без default=false: %#v", visible)
	}
	if _, required := copy["required"]; required {
		t.Fatal("visible должен оставаться необязательным для прежнего поведения")
	}
}
