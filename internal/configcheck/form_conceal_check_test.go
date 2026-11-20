package configcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RunFull is the public validation path used by onebase check.
func TestRunFullConcealContract(t *testing.T) {
	cases := []struct {
		name, typ, path, expression, extra string
		valid                              bool
	}{
		{"plain", "string", "Объект.Секрет", "НЕ Маской", "", true},
		{"multiline", "string", "Объект.Секрет", "NOT Маской", "  multiline: true\n", true},
		{"readonly", "string", "Объект.Секрет", "Маской", "  readonly: true\n", true},
		{"permanent", "string", "Объект.Секрет", "", "", true},
		{"number", "number", "Объект.Секрет", "Маской", "", false},
		{"date", "date", "Объект.Секрет", "Маской", "", false},
		{"bool", "bool", "Объект.Секрет", "Маской", "", false},
		{"reference", "reference:Контрагент", "Объект.Секрет", "Маской", "", false},
		{"enum", "enum:Виды", "Объект.Секрет", "Маской", "", false},
		{"richtext", "richtext", "Объект.Секрет", "Маской", "", false},
		{"image", "image", "Объект.Секрет", "Маской", "", false},
		{"file", "string", "Объект.Секрет", "Маской", "  type: file\n", false},
		{"form attribute", "string", "Маской", "Маской", "", false},
		{"table path", "string", "Объект.Строки.Секрет", "Маской", "", false},
		{"expression", "string", "Объект.Секрет", "Маской И Истина", "", false},
		{"unknown", "string", "Объект.Секрет", "НетРеквизита", "", false},
		{"duplicate", "string", "Объект.Секрет", "Маской", "- kind: ПолеВвода\n  name: Копия\n  data_path: объект.секрет\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{
				"config/app.yaml":                    "name: test\n",
				"catalogs/контрагент.yaml":           "name: Контрагент\nfields:\n- name: Секрет\n  type: " + tc.typ + "\n",
				"forms/контрагент/объекта.form.yaml": "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Контрагент\nattributes:\n- name: Маской\n  type: bool\nelements:\n- kind: ПолеВвода\n  name: Поле\n  data_path: " + tc.path + "\n  conceal: true\n  reveal_when: \"" + tc.expression + "\"\n" + tc.extra,
			}
			for name, body := range files {
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			result := RunFull(dir)
			found := false
			for _, issue := range result.Issues {
				if issue.Code == "form.conceal" {
					found = true
				}
			}
			if found == tc.valid {
				t.Fatalf("valid=%v concealError=%v result=%+v", tc.valid, found, result)
			}
			if tc.valid {
				for _, issue := range result.Issues {
					if strings.Contains(issue.Message, "form.yaml") {
						t.Fatalf("fixture failed to load: %+v", result)
					}
				}
			}
		})
	}
}
