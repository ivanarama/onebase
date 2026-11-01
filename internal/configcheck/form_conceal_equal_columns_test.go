package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRunFullWithLint_ConcealAndEqualColumns(t *testing.T) {
	for _, variant := range []string{"plain", "multiline", "readonly", "equal_column", "conceall", "reveal_whenn", "scope"} {
		t.Run(variant, func(t *testing.T) {
			dir := t.TempDir()
			mkFile(t, filepath.Join(dir, "config/app.yaml"), "name: test\n")
			mkFile(t, filepath.Join(dir, "catalogs/образец.yaml"), "name: Образец\nfields:\n- name: Значение\n  type: string\n")
			body := `schema: onebase.form/v1
form:
  name: Форма
  kind: object
  entity: Образец
attributes:
- name: Маской
  type: bool
elements:
- kind: ГруппаФормы
  name: Группа
  orientation: horizontal
  equal_columns: true
  children:
  - kind: ПолеВвода
    name: Поле
    data_path: Объект.Значение
    conceal: true
    reveal_when: НЕ Маской
`
			switch variant {
			case "multiline":
				body = strings.Replace(body, "НЕ Маской", "NOT Маской\n    multiline: true", 1)
			case "readonly":
				body += "    readonly: true\n"
			case "equal_column", "conceall", "reveal_whenn":
				key := map[string]string{"equal_column": "equal_columns", "conceall": "conceal", "reveal_whenn": "reveal_when"}[variant]
				body = strings.Replace(body, key+":", variant+":", 1)
			case "scope":
				body = strings.Replace(body, "  kind: object", "  kind: object\n  conceal: true\n  reveal_when: Маской\n  equal_columns: true", 1)
			}
			mkFile(t, filepath.Join(dir, "forms/образец/объекта.form.yaml"), body)
			result := RunFullWithOptions(dir, Options{Lint: true})
			invalidKey := false
			for _, warning := range result.Warnings {
				if warning.Code == "metadata.unvalidated-key" {
					invalidKey = true
				}
			}
			valid := variant == "plain" || variant == "multiline" || variant == "readonly"
			if invalidKey == valid || (valid && !result.OK) {
				t.Fatalf("valid=%v unknown-key=%v: %+v", valid, invalidKey, result)
			}
		})
	}
}
