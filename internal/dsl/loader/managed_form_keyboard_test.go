package loader

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagedFormLoader_KeyboardKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ФормаОбъекта.form.yaml")
	src := "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Звонок\n  tab_skip_readonly: true\n  autofocus: first_empty\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	form, err := NewManagedFormLoader().LoadFormFile(path, "Звонок")
	if err != nil {
		t.Fatal(err)
	}
	if !form.TabSkipReadonly || form.Autofocus != "first_empty" {
		t.Fatalf("ключи не загружены: tab_skip_readonly=%v autofocus=%q", form.TabSkipReadonly, form.Autofocus)
	}
}
