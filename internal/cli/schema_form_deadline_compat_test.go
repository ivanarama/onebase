package cli

import (
	"encoding/json"
	"testing"
)

func TestSchemaFormDeadlineAndMainKeys(t *testing.T) {
	out, err := captureStdout(t, func() error { _, err := executeRootArgs(t, "schema", "form"); return err })
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	props := schemaAt(t, doc, "properties", "elements", "items", "properties")
	deadline := schemaAt(t, props, "deadline_warn")
	if deadline["type"] != "integer" || deadline["minimum"] != float64(1) {
		t.Fatalf("deadline schema: %#v", deadline)
	}
	for _, key := range []string{"equal_columns", "hint_inline", "conceal"} {
		if schemaAt(t, props, key)["type"] != "boolean" {
			t.Fatalf("lost boolean %s", key)
		}
	}
	for _, key := range []string{"reveal_when"} {
		if schemaAt(t, props, key)["type"] != "string" {
			t.Fatalf("lost string %s", key)
		}
	}
}
