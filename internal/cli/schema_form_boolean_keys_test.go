package cli

import (
	"encoding/json"
	"testing"
)

func TestSchemaFormPublishesBooleanElementKeys(t *testing.T) {
	out, err := captureStdout(t, func() error {
		_, err := executeRootArgs(t, "schema", "form")
		return err
	})
	if err != nil {
		t.Fatalf("onebase schema form: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("schema form returned invalid JSON: %v", err)
	}
	element := schemaAt(t, doc, "properties", "elements", "items")
	if element["additionalProperties"] != true {
		t.Fatal("historical element keys must remain accepted")
	}
	if _, required := element["required"]; required {
		t.Fatal("element keys must remain optional")
	}
	for _, key := range []string{"choice_folders", "choice_dropdown", "editable_admin_only", "primary", "scroll_x"} {
		t.Run(key, func(t *testing.T) {
			property := schemaAt(t, element, "properties", key)
			if property["type"] != "boolean" {
				t.Fatalf("%s must accept booleans and reject strings: %#v", key, property)
			}
			// A bare boolean type accepts both true and false. Do not accidentally
			// constrain one of them, or turn the optional dropdown into a default false.
			for _, constraint := range []string{"enum", "const", "default"} {
				if _, present := property[constraint]; present {
					t.Fatalf("unexpected %s on %s: %#v", constraint, key, property)
				}
			}
			if description, ok := property["description"].(string); !ok || description == "" {
				t.Fatalf("%s has no behavior description", key)
			}
		})
	}
	children := schemaAt(t, element, "properties", "children")
	if children["type"] != "array" || element["$dynamicAnchor"] != "formElement" ||
		schemaAt(t, children, "items")["$dynamicRef"] != "#formElement" {
		t.Fatal("nested children must reuse the typed element schema")
	}
}
