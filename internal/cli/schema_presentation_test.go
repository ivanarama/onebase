package cli

import "testing"

func TestEntitySchemaPresentationRejectsEmptyAndDuplicateCandidates(t *testing.T) {
	entity := allSchemas()["entity"]
	properties := entity["properties"].(map[string]any)
	presentation := properties["presentation"].(map[string]any)
	variants, ok := presentation["oneOf"].([]any)
	if !ok || len(variants) != 2 {
		t.Fatalf("presentation must accept scalar and list: %#v", presentation)
	}
	scalar := variants[0].(map[string]any)
	if scalar["type"] != "string" || scalar["minLength"] != 1 {
		t.Fatalf("scalar presentation accepts an empty field name: %#v", scalar)
	}
	list := variants[1].(map[string]any)
	if list["type"] != "array" || list["minItems"] != 1 || list["uniqueItems"] != true {
		t.Fatalf("presentation list accepts empty/duplicate candidates: %#v", list)
	}
	items := list["items"].(map[string]any)
	if items["type"] != "string" || items["minLength"] != 1 {
		t.Fatalf("presentation list accepts an empty field name: %#v", items)
	}
}
