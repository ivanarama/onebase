package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedDisplayTitleLintEntryPoint(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "item.yaml"), `name: Item
fields:
  - name: Name
    type: string
`)
	mkFile(t, filepath.Join(dir, "forms", "item", "object.form.yaml"), `schema: onebase.form/v1
form:
  name: Object
  entity: Item
  kind: object
  title_proc: DisplayTitle
elements: []
`)
	mkFile(t, filepath.Join(dir, "forms", "item", "object.form.os"), `Функция DisplayTitle()
  Возврат TitleHelper();
КонецФункции
Функция TitleHelper()
  Возврат "Safe";
КонецФункции
Функция UnusedHelper()
  Возврат "Unused";
КонецФункции
`)
	res := RunFullWithOptions(dir, Options{Lint: true})
	if !res.OK {
		t.Fatalf("check: %+v", res.Issues)
	}
	unusedFound := false
	for _, warning := range res.Warnings {
		if warning.Code == "metadata.unvalidated-key" && strings.Contains(warning.Message, "title_proc") {
			t.Errorf("runtime title key rejected: %+v", warning)
		}
		if warning.Code == "dsl.dead-procedure" {
			if strings.Contains(warning.Message, "UnusedHelper") {
				unusedFound = true
			} else {
				t.Errorf("runtime title function or its helper marked dead: %+v", warning)
			}
		}
	}
	if !unusedFound {
		t.Error("unreachable helper should still be reported")
	}
}
