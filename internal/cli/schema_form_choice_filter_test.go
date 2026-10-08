package cli

import (
	"encoding/json"
	"testing"
)

// Тест идёт через вывод публичной команды: именно `onebase schema form`
// читают редакторы и агенты, а не внутреннюю карту allSchemas.
func TestSchemaFormPublishesChoiceFilterContract(t *testing.T) {
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
	choice := schemaAt(t, doc, "properties", "elements", "items", "properties", "choice_filter")
	if choice["type"] != "array" || choice["minItems"] != float64(1) || choice["maxItems"] != float64(8) {
		t.Fatalf("choice_filter bounds = %#v", choice)
	}
	condition := schemaAt(t, choice, "items")
	if condition["additionalProperties"] != false {
		t.Fatalf("condition must reject unknown keys: %#v", condition)
	}
	properties := schemaAt(t, condition, "properties")
	op := schemaAt(t, properties, "op")
	values, ok := op["enum"].([]any)
	if !ok || len(values) != 4 || values[0] != "eq" || values[1] != "eq_or_empty" || values[2] != "in_hierarchy" || values[3] != "not_in_hierarchy" {
		t.Fatalf("operator enum = %#v", op["enum"])
	}
	if schemaAt(t, properties, "value")["type"] != "boolean" {
		t.Fatalf("choice_filter.value is not boolean: %#v", properties["value"])
	}
	if schemaAt(t, properties, "field")["type"] != "string" || schemaAt(t, properties, "from")["type"] != "string" {
		t.Fatalf("field/from types are not strings: %#v", properties)
	}
	// ref (#1820) — третий взаимоисключающий источник: строка-UUID, и ровно
	// одна из трёх веток oneOf требует именно его.
	ref := schemaAt(t, properties, "ref")
	if ref["type"] != "string" || ref["format"] != "uuid" {
		t.Fatalf("choice_filter.ref is not a UUID string: %#v", ref)
	}
	branches, ok := condition["oneOf"].([]any)
	if !ok || len(branches) != 3 {
		t.Fatalf("condition must require exactly one of from/value/ref: %#v", condition["oneOf"])
	}
	required := map[string]bool{}
	for _, raw := range branches {
		branch, _ := raw.(map[string]any)
		names, _ := branch["required"].([]any)
		if len(names) != 1 {
			t.Fatalf("oneOf branch must require one source: %#v", branch)
		}
		required[names[0].(string)] = true
	}
	if !required["from"] || !required["value"] || !required["ref"] {
		t.Fatalf("oneOf branches = %#v", required)
	}
	element := schemaAt(t, doc, "properties", "elements", "items")
	children := schemaAt(t, element, "properties", "children", "items")
	if children["$dynamicRef"] != "#formElement" || element["$dynamicAnchor"] != "formElement" {
		t.Fatalf("nested form elements do not reuse the same schema: element=%#v children=%#v", element, children)
	}
}
