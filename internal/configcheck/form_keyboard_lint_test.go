package configcheck

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestLint_FormKeyboardKeyPlacement(t *testing.T) {
	for _, key := range []struct {
		name  string
		value string
	}{
		{"tab_skip_readonly", "true"},
		{"autofocus", "first_empty"},
	} {
		t.Run(key.name, func(t *testing.T) {
			for _, placement := range []struct {
				name   string
				root   bool
				header bool
			}{
				{"root", true, false},
				{"form", false, true},
				{"root_and_form", true, true},
			} {
				t.Run(placement.name, func(t *testing.T) {
					dir := t.TempDir()
					mkFile(t, filepath.Join(dir, "documents", "заказ.yaml"), "name: Заказ\nfields:\n  - name: Номер\n    type: string\n")
					var root, header string
					if placement.root {
						root = fmt.Sprintf("%s: %s\n", key.name, key.value)
					}
					if placement.header {
						header = fmt.Sprintf("  %s: %s\n", key.name, key.value)
					}
					mkFile(t, filepath.Join(dir, "forms", "заказ", "объекта.form.yaml"), fmt.Sprintf(`schema: onebase.form/v1
%sform:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
%selements:
  - kind: ПолеВвода
    name: Номер
    data_path: Объект.Номер
`, root, header))

					// Exercise the same public entry point as onebase check --lint.
					res := RunFullWithOptions(dir, Options{Lint: true})
					if !res.OK {
						t.Fatalf("valid form fixture failed to load: %+v", res.Issues)
					}
					if got := unvalidatedKeyMentions(res, key.name); got != placement.root {
						t.Errorf("unknown-key diagnostic = %v, want %v; issues: %+v; warnings: %+v", got, placement.root, res.Issues, res.Warnings)
					}
				})
			}
		})
	}
}
