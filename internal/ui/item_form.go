package ui

import (
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// splitItemFormFields делит реквизиты шапки на видимые и скрытые по блоку
// `item_form:` (план 117, Д12). Пустой блок — видно всё, как было.
//
// Порядок видимых задаётся самим списком: это не только фильтр, но и
// расположение — ровно так же работает `list_form:` для колонок списка.
// Неизвестное имя в списке молча пропускается: ронять форму из-за опечатки в
// необязательном ключе хуже, чем показать реквизит; за опечатками следит линт.
func splitItemFormFields(entity *metadata.Entity) (visible, hidden []metadata.Field) {
	if entity == nil {
		return nil, nil
	}
	if len(entity.ItemForm) == 0 {
		return entity.Fields, nil
	}
	want := make(map[string]int, len(entity.ItemForm))
	for i, name := range entity.ItemForm {
		want[strings.ToLower(strings.TrimSpace(name))] = i
	}
	visible = make([]metadata.Field, 0, len(entity.ItemForm))
	order := make([]int, 0, len(entity.ItemForm))
	for _, f := range entity.Fields {
		if pos, ok := want[strings.ToLower(f.Name)]; ok {
			visible = append(visible, f)
			order = append(order, pos)
			continue
		}
		hidden = append(hidden, f)
	}
	// Сортировка вставками по позиции в item_form: список короткий, а порядок
	// объявления автор задал осознанно.
	for i := 1; i < len(visible); i++ {
		f, pos := visible[i], order[i]
		j := i - 1
		for j >= 0 && order[j] > pos {
			visible[j+1], order[j+1] = visible[j], order[j]
			j--
		}
		visible[j+1], order[j+1] = f, pos
	}
	return visible, hidden
}
