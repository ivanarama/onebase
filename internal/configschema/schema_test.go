package configschema

import (
	"encoding/json"
	"testing"
)

func TestGetSchemaAliases(t *testing.T) {
	s, ok := Get("документ")
	if !ok {
		t.Fatal("document alias not found")
	}
	if s["$schema"] == "" || s["type"] != "object" {
		t.Fatalf("schema looks wrong: %+v", s)
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("schema must be JSON-marshalable: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatalf("schema JSON invalid: %s", raw)
	}
}

func TestKindsSorted(t *testing.T) {
	kinds := Kinds()
	if len(kinds) < 10 {
		t.Fatalf("too few schema kinds: %v", kinds)
	}
	for i := 1; i < len(kinds); i++ {
		if kinds[i-1] > kinds[i] {
			t.Fatalf("kinds not sorted: %v", kinds)
		}
	}
	for _, want := range []string{"catalog", "document", "form", "role", "service", "widget"} {
		var found bool
		for _, got := range kinds {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("kind %q missing in %v", want, kinds)
		}
	}
}
