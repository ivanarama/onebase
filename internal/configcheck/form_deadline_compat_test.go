package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// The public check path must accept both sides of the integration, while
// continuing to diagnose a misspelled neighboring key.
func TestRunFullWithLintDeadlineAndEqualColumns(t *testing.T) {
	for _, typo := range []string{"", "deadline_war", "equal_column"} {
		t.Run("key="+typo, func(t *testing.T) {
			dir := t.TempDir()
			mkFile(t, filepath.Join(dir, "config/app.yaml"), "name: test\n")
			mkFile(t, filepath.Join(dir, "catalogs/образец.yaml"), "name: Образец\nfields:\n- name: Срок\n  type: string\n")
			body := `schema: onebase.form/v1
form:
  name: Форма
  kind: object
  entity: Образец
elements:
- kind: ГруппаФормы
  name: Группа
  orientation: horizontal
  equal_columns: true
  children:
  - kind: ПолеВвода
    name: ПолеСрок
    data_path: Объект.Срок
    deadline_warn: 30
`
			if typo != "" {
				correct := map[string]string{"deadline_war": "deadline_warn", "equal_column": "equal_columns"}[typo]
				body = strings.Replace(body, correct+":", typo+":", 1)
			}
			mkFile(t, filepath.Join(dir, "forms/образец/объекта.form.yaml"), body)
			result := RunFullWithOptions(dir, Options{Lint: true})
			if !result.OK {
				t.Fatalf("check failed: %+v", result)
			}
			var unknown []string
			for _, warning := range result.Warnings {
				if warning.Code == "metadata.unvalidated-key" {
					unknown = append(unknown, warning.Message)
				}
			}
			if typo == "" && len(unknown) != 0 {
				t.Fatalf("supported keys rejected: %v", unknown)
			}
			if typo != "" && (len(unknown) != 1 || !strings.Contains(unknown[0], typo)) {
				t.Fatalf("typo %q not diagnosed: %v", typo, unknown)
			}
		})
	}
}
