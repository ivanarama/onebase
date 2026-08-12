package cli

import "testing"

func TestEntitySchemaDetailPanelIsClosedAndTyped(t *testing.T) {
	entity := allSchemas()["entity"]
	properties, ok := entity["properties"].(map[string]any)
	if !ok {
		t.Fatalf("entity properties = %#v", entity["properties"])
	}
	detail, ok := properties["detail_panel"].(map[string]any)
	if !ok {
		t.Fatalf("detail_panel schema = %#v", properties["detail_panel"])
	}
	if detail["additionalProperties"] != false {
		t.Fatalf("detail_panel accepts misspelled keys: %#v", detail)
	}
	if _, ok := detail["allOf"].([]any); !ok {
		t.Fatalf("detail_panel schema does not forbid fields+tabs: %#v", detail)
	}
	detailProps := detail["properties"].(map[string]any)
	width := detailProps["width"].(map[string]any)
	if _, ok := width["anyOf"].([]any); !ok {
		t.Fatalf("width must express 0/default or bounded px value: %#v", width)
	}
	tabs := detailProps["tabs"].(map[string]any)
	if tabs["minItems"] != 1 {
		t.Fatalf("detail_panel.tabs allows fail-open empty list: %#v", tabs)
	}
	tab := tabs["items"].(map[string]any)
	if tab["additionalProperties"] != false {
		t.Fatalf("detail_panel.tabs[] accepts misspelled keys: %#v", tab)
	}
	required, ok := tab["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "name" {
		t.Fatalf("detail_panel.tabs[].name is not required: %#v", tab["required"])
	}
	tabProps := tab["properties"].(map[string]any)
	titles := tabProps["titles"].(map[string]any)
	additional, ok := titles["additionalProperties"].(map[string]any)
	if !ok || additional["type"] != "string" {
		t.Fatalf("detail_panel.tabs[].titles is not a string map: %#v", titles)
	}
	for _, key := range []string{"fields", "tableparts", "attachments"} {
		if _, ok := tabProps[key]; !ok {
			t.Fatalf("detail_panel.tabs[] schema lost %q: %#v", key, tabProps)
		}
	}
}
