package ui

import (
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// itemFormField — реквизит формы элемента с признаком «только просмотр»
// (#1011). Встраивание metadata.Field, а не копия полей: шаблон обращается к
// .Name/.Type/.DisplayName как раньше, а рядом появляется .ReadOnly.
type itemFormField struct {
	metadata.Field
	ReadOnly bool
}

// splitItemFormFields делит реквизиты шапки на видимые и скрытые по блоку
// `item_form:` (план 117, Д12). Пустой блок — видно всё, как было.
//
// Порядок видимых задаётся самим списком: это не только фильтр, но и
// расположение — ровно так же работает `list_form:` для колонок списка.
// Неизвестное имя в списке молча пропускается: ронять форму из-за опечатки в
// необязательном ключе хуже, чем показать реквизит; за опечатками следит линт.
//
// Запись вида `{name: X, readonly: true}` помечает реквизит «только
// просмотр»: поле видно, но не редактируется. Скрытым такой признак не нужен —
// они и так едут hidden-полями.
func splitItemFormFields(entity *metadata.Entity) (visible []itemFormField, hidden []metadata.Field) {
	if entity == nil {
		return nil, nil
	}
	if len(entity.ItemForm) == 0 {
		visible = make([]itemFormField, 0, len(entity.Fields))
		for _, f := range entity.Fields {
			visible = append(visible, itemFormField{Field: f})
		}
		return visible, nil
	}
	type want struct {
		pos      int
		readOnly bool
	}
	wanted := make(map[string]want, len(entity.ItemForm))
	for i, f := range entity.ItemForm {
		wanted[strings.ToLower(strings.TrimSpace(f.Name))] = want{pos: i, readOnly: f.ReadOnly}
	}
	visible = make([]itemFormField, 0, len(entity.ItemForm))
	order := make([]int, 0, len(entity.ItemForm))
	for _, f := range entity.Fields {
		if w, ok := wanted[strings.ToLower(f.Name)]; ok {
			visible = append(visible, itemFormField{Field: f, ReadOnly: w.readOnly})
			order = append(order, w.pos)
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
