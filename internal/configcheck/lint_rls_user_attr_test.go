package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// user_attr с небазовым именем. Собственных атрибутов пользователя платформа не
// хранит, и политика с таким атрибутом не разрешает ни одной строки. Раньше
// предупреждение говорило только «unknown row policy user_attr», а документация
// обещала атрибуты «от хоста», задать которые негде: автор политики искал, где
// их заполнить. Предупреждение обязано назвать встроенные атрибуты и сказать,
// что собственных нет.
func TestLintRoles_RowAccessCustomUserAttrExplained(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "клиент.yaml"), `name: Клиент
fields:
  - name: Подразделение
    type: string
`)
	mkFile(t, filepath.Join(dir, "roles", "manager.yaml"), `name: Manager
permissions:
  catalogs:
    Клиент: [read]
  row_access:
    catalogs:
      Клиент:
        read:
          field: Подразделение
          op: eq
          value: { user_attr: Подразделение }
`)

	res := RunFullWithOptions(dir, Options{Lint: true})
	if !res.OK {
		t.Fatalf("row_access lint warnings should not fail check: %+v", res.Issues)
	}
	var msg string
	for _, w := range res.Warnings {
		if w.Code == "rls.invalid-policy" {
			msg = w.Message
		}
	}
	if msg == "" {
		t.Fatalf("ожидалось предупреждение rls.invalid-policy, получено: %+v", res.Warnings)
	}
	for _, want := range []string{`"подразделение"`, "login", "full_name", "ai_data_access", "not stored"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("в предупреждении нет %q: %s", want, msg)
		}
	}
}
