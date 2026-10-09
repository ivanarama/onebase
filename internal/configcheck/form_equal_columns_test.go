package configcheck

import (
	"path/filepath"
	"testing"
)

func TestRunFull_EqualColumnsUsage(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "клиент.yaml"), "name: Клиент\nfields:\n  - name: Наименование\n    type: string\n")
	mkFile(t, filepath.Join(dir, "forms", "клиент", "форма.form.yaml"), `schema: onebase.form/v1
form:
  name: Форма
  kind: object
  entity: Клиент
elements:
  - kind: ГруппаФормы
    name: Правильная
    orientation: horizontal
    equal_columns: true
    children:
      - kind: ПолеВвода
        name: Поле
        data_path: Объект.Наименование
  - kind: ГруппаФормы
    name: Вертикальная
    equal_columns: true
  - kind: ПолеВвода
    name: ЧужойВид
    data_path: Объект.Наименование
    equal_columns: true
  - kind: ГруппаФормы
    name: Прокручиваемая
    orientation: horizontal
    scroll_x: true
    equal_columns: true
`)
	result := RunFull(dir)
	count := 0
	for _, w := range result.Warnings {
		if w.Code == "form.equal-columns" {
			count++
			if w.File != "forms/клиент/форма.form.yaml" {
				t.Errorf("wrong locator: %+v", w)
			}
		}
	}
	if count != 3 || !result.OK {
		t.Fatalf("expected 3 warnings and no errors: %+v", result)
	}
}
