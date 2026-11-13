package configcheck

import (
	"path/filepath"
	"testing"
)

// form.roles / form.except_roles модель читает — линт не должен называть их
// неизвестными.
func TestLintYAML_FormRolesKnown(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "forms", "задача", "компактная.form.yaml"), `schema: onebase.form/v1
form:
  name: Компактная
  kind: object
  entity: Задача
  roles: [СотрудникОТК, РуководительОТК]
  except_roles: [ОператорКЦ]
elements: []
`)
	for _, issue := range CheckLintYAML(dir) {
		if issue.Code == "metadata.unvalidated-key" {
			t.Fatalf("roles/except_roles объявлены неизвестными: %+v", issue)
		}
	}
}
