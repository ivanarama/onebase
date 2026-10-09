package configcheck

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunFull_FormReadonlyConflict(t *testing.T) {
	for _, owner := range []struct {
		dir, name, kind, fields string
	}{
		{"catalogs", "Заказ", "object", "fields"},
		{"documents", "Заказ", "object", "fields"},
		{"processors", "Заказ", "custom", "params"},
	} {
		t.Run(owner.dir, func(t *testing.T) {
			dir := t.TempDir()
			mkFile(t, filepath.Join(dir, owner.dir, "заказ.yaml"), fmt.Sprintf("name: %s\n%s:\n  - name: Состояние\n    type: string\n", owner.name, owner.fields))
			mkFile(t, filepath.Join(dir, "forms", "заказ", "основная.form.yaml"), fmt.Sprintf(`schema: onebase.form/v1
form:
  name: основная
  kind: %s
  entity: %s
elements:
  - kind: ПолеВвода
    name: ЯвныйКонфликт
    data_path: Состояние
    readonly: true
    readonly_when: Ложь
  - kind: ГруппаФормы
    name: Вложенность
    children:
      - kind: ПолеВвода
        name: ВложенныйКонфликт
        data_path: Состояние
        readonly: true
        readonly_when: Истина
  - kind: ГруппаФормы
    name: КонфликтКонтейнера
    readonly: true
    readonly_when: Истина
    children:
      - kind: ПолеВвода
        name: ТолькоУнаследованныйЗапрет
        data_path: Состояние
        readonly_when: Истина
  - kind: ПолеВвода
    name: ТолькоПостоянныйЗапрет
    data_path: Состояние
    readonly: true
  - kind: ПолеВвода
    name: ТолькоУсловныйЗапрет
    data_path: Состояние
    readonly_when: Истина
  - kind: ПолеВвода
    name: ЯвноРедактируемое
    data_path: Состояние
    readonly: false
    readonly_when: Истина
  - kind: ПолеВвода
    name: ПустоеУсловие
    data_path: Состояние
    readonly: true
    readonly_when: ''
  - kind: ПолеВвода
    name: ПробельноеУсловие
    data_path: Состояние
    readonly: true
    readonly_when: '   '
`, owner.kind, owner.name))

			res := RunFull(dir)
			if !res.OK {
				t.Fatalf("предупреждение не должно блокировать check: %+v", res.Issues)
			}
			var got []Issue
			for _, w := range res.Warnings {
				if w.Code == "form.readonly-conflict" {
					got = append(got, w)
				}
			}
			wantNames := []string{"ЯвныйКонфликт", "ВложенныйКонфликт", "КонфликтКонтейнера"}
			if len(got) != len(wantNames) {
				t.Fatalf("предупреждений = %d, ожидалось %d: %+v", len(got), len(wantNames), res.Warnings)
			}
			for index, w := range got {
				if w.File != "forms/заказ/основная.form.yaml" || w.Object != owner.name || w.Kind != "Управляемая форма" {
					t.Errorf("неверный локатор: %+v", w)
				}
				if !strings.Contains(w.Message, wantNames[index]) || !strings.Contains(w.Message, "readonly: true") || !strings.Contains(w.Message, "readonly_when") || w.SuggestedFix == "" {
					t.Errorf("нет имени элемента, причины или подсказки: %+v", w)
				}
			}
		})
	}
}
