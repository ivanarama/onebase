package configcheck

import (
	"path/filepath"
	"testing"
)

// Exercise the CLI check/lint entry point with the keys from both branches.
func TestCheck_ListFormFilterWithMainFormSettings(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "заявка.yaml"), `name: Заявка
fields:
  - {name: Наименование, type: string}
  - {name: Автор, type: string}
`)
	mkFile(t, filepath.Join(dir, "forms", "заявка", "списка.form.yaml"), `schema: onebase.form/v1
form:
  name: ФормаСписка
  kind: list
  entity: Заявка
  ref_card_button: false
  ref_card_button_admin_only: true
  enter_submits_form: true
  filter:
    - field: Автор
      op: eq
      value: "@ТекущийПользователь"
      roles: [Оператор]
    - query: ВЫБРАТЬ З.Ссылка ИЗ Справочник.Заявка КАК З ГДЕ З.Автор = &ТекущийПользователь
      removable: true
      label: {ru: Только мои}
`)
	result := RunFullWithOptions(dir, Options{Lint: true})
	if !result.OK {
		t.Fatalf("combined form rejected: %+v", result.Issues)
	}
	for _, key := range []string{"filter", "field", "op", "value", "roles", "query", "removable", "label", "ref_card_button", "ref_card_button_admin_only", "enter_submits_form"} {
		if unvalidatedKeyMentions(result, key) {
			t.Errorf("supported key %q reported unknown: %+v", key, result.Warnings)
		}
	}
}
