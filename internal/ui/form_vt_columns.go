package ui

import (
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// managedVTColumn — колонка ValueTable в плане показа: та же идея, что у
// managedTPColumn для табличной части.
type managedVTColumn struct {
	Column   *metadata.FormAttributeColumn
	Element  *metadata.FormElement
	Hidden   bool
	ReadOnly bool
}

// Title — подпись колонки: подпись элемента-ребёнка важнее объявления атрибута.
func (c managedVTColumn) Title() string {
	if c.Element != nil {
		if title := strings.TrimSpace(c.Element.TitleMap["ru"]); title != "" {
			return title
		}
	}
	if c.Column != nil {
		if title := strings.TrimSpace(c.Column.Title["ru"]); title != "" {
			return title
		}
		return c.Column.Name
	}
	return ""
}

// managedVTColumnPlan раскладывает колонки ValueTable в порядок и состав,
// заданные детьми kind: Колонка, — ровно как managedTPColumnPlan делает это для
// реквизитов табличной части.
//
// До этого ValueTable показывала ВСЕ объявленные колонки, и спрятать служебную
// (скажем, идентификатор выбранной записи) было нечем: приходилось показывать
// пользователю UUID. Невыбранные колонки уходят в конец плана скрытыми — их
// ячейки остаются в разметке, иначе значение не доехало бы обратно на сервер.
//
// Ни одного ребёнка-колонки — план равен объявлению атрибута: «ничего не
// выбрано» значит «показать всё», как и у табличной части.
func managedVTColumnPlan(el *metadata.FormElement, cols []*metadata.FormAttributeColumn) []managedVTColumn {
	full := func() []managedVTColumn {
		plan := make([]managedVTColumn, 0, len(cols))
		for _, column := range cols {
			plan = append(plan, managedVTColumn{Column: column})
		}
		return plan
	}
	if el == nil {
		return full()
	}

	chosen := make(map[int]*metadata.FormElement, len(cols))
	order := make([]int, 0, len(cols))
	for _, child := range el.Children {
		if child == nil || child.Kind != metadata.FormElementColumn {
			continue
		}
		index, ok := managedVTColumnIndex(cols, child)
		if !ok {
			continue
		}
		// Одна колонка, объявленная дважды, показывается один раз: две ячейки
		// с одинаковым name= дали бы неоднозначный payload.
		if _, duplicate := chosen[index]; duplicate {
			continue
		}
		chosen[index] = child
		order = append(order, index)
	}
	if len(order) == 0 {
		return full()
	}

	plan := make([]managedVTColumn, 0, len(cols))
	for _, index := range order {
		child := chosen[index]
		plan = append(plan, managedVTColumn{Column: cols[index], Element: child, ReadOnly: child.ReadOnly})
	}
	for index, column := range cols {
		if _, shown := chosen[index]; shown {
			continue
		}
		plan = append(plan, managedVTColumn{Column: column, Hidden: true, ReadOnly: true})
	}
	return plan
}

// managedVTColumnIndex ищет колонку атрибута, на которую ссылается элемент
// kind: Колонка, — по data_path, имени реквизита или имени элемента.
func managedVTColumnIndex(cols []*metadata.FormAttributeColumn, column *metadata.FormElement) (int, bool) {
	if column == nil {
		return 0, false
	}
	for _, candidate := range []string{
		strings.TrimSpace(dpFieldName(strings.TrimSpace(column.DataPath))),
		strings.TrimSpace(column.FieldName),
		strings.TrimSpace(column.Name),
	} {
		if candidate == "" {
			continue
		}
		for index, col := range cols {
			if col != nil && strings.EqualFold(strings.TrimSpace(col.Name), candidate) {
				return index, true
			}
		}
	}
	return 0, false
}

// managedVTEditable сообщает, есть ли в плане хоть одна доступная для правки
// колонка. Таблица только для чтения не должна предлагать «+ Добавить строку»
// и крестики удаления: строки в ней появляются из обработчика, а не руками.
func managedVTEditable(plan []managedVTColumn) bool {
	for _, column := range plan {
		if !column.Hidden && !column.ReadOnly {
			return true
		}
	}
	return false
}
