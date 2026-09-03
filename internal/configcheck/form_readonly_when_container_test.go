package configcheck

import (
	"fmt"
	"path/filepath"
	"testing"
)

// Условная нередактируемость контейнера — законная конфигурация: условие на
// ГруппеФормы/СтраницахФормы/Странице каскадит на потомков так же, как
// статический readonly (#1184). Пока каскада не было, check отвергал такую
// форму отдельной проверкой (CheckFormReadOnlyWhen, план 158): контейнер
// выглядел запретом всей области, а дочерние поля оставались редактируемыми.
// Проверка снята вместе с появлением каскада, и этот тест держит границу с
// другой стороны — через ту же публичную точку входа, что и `onebase check`.
func TestRunFull_ReadonlyWhenNaKonteynerahPrinimaetsya(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "заказ.yaml"), `name: Заказ
fields:
  - name: Состояние
    type: string
`)
	mkFile(t, filepath.Join(dir, "processors", "проверкаформы.yaml"), `name: ПроверкаФормы
params:
  - name: Состояние
    type: string
`)
	form := `schema: onebase.form/v1
form:
  name: %s
  kind: %s
  entity: %s
elements:
  - kind: ГруппаФормы
    name: Группа
    readonly_when: 'Состояние = "Закрыт"'
    children:
      - kind: ПолеВвода
        name: ПолеВГруппе
        readonly_when: 'Состояние = "Закрыт"'
  - kind: СтраницыФормы
    name: Страницы
    readonly_when: 'Состояние = "Закрыт"'
    children:
      - kind: Страница
        name: Страница
        readonly_when: 'Состояние = "Закрыт"'
        children:
          - kind: ТабличнаяЧасть
            name: Таблица
`
	mkFile(t, filepath.Join(dir, "forms", "заказ", "объекта.form.yaml"),
		fmt.Sprintf(form, "объекта", "object", "Заказ"))
	mkFile(t, filepath.Join(dir, "forms", "проверкаформы", "основная.form.yaml"),
		fmt.Sprintf(form, "основная", "custom", "ПроверкаФормы"))

	res := RunFull(dir)
	if !res.OK {
		t.Fatalf("check отклонил readonly_when на контейнерах: %+v", res.Issues)
	}
}
