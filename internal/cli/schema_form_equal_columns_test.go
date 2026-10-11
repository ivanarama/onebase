package cli

import (
	"encoding/json"
	"testing"
)

func TestSchemaFormPublishesEqualColumns(t *testing.T) {
	out, err := captureStdout(t, func() error { _, err := executeRootArgs(t, "schema", "form"); return err })
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	property := schemaAt(t, doc, "properties", "elements", "items", "properties", "equal_columns")
	if property["type"] != "boolean" {
		t.Fatalf("equal_columns: %#v", property)
	}
}
