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
	if !ok || len(values) != 2 || values[0] != "eq" || values[1] != "in_hierarchy" {
		t.Fatalf("operator enum = %#v", op["enum"])
	}
	if schemaAt(t, properties, "value")["type"] != "boolean" {
		t.Fatalf("choice_filter.value is not boolean: %#v", properties["value"])
	}
	if schemaAt(t, properties, "field")["type"] != "string" || schemaAt(t, properties, "from")["type"] != "string" {
		t.Fatalf("field/from types are not strings: %#v", properties)
	}
	element := schemaAt(t, doc, "properties", "elements", "items")
	children := schemaAt(t, element, "properties", "children", "items")
	if children["$dynamicRef"] != "#formElement" || element["$dynamicAnchor"] != "formElement" {
		t.Fatalf("nested form elements do not reuse the same schema: element=%#v children=%#v", element, children)
	}
}
