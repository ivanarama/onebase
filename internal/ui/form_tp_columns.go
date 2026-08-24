package ui

import (
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// managedTPColumn — одна колонка табличной части в порядке показа.
//
// Hidden означает «не показывать», а НЕ «не отправлять». Значение скрытой
// колонки обязано доехать до сервера в неизменном виде:
// convertManagedTablePartRows идёт по ВСЕМ реквизитам табличной части и
// подставляет пустое значение тому, чего нет в присланной строке. Колонка,
// выпавшая из полезной нагрузки, затирается в базе при каждой записи — молча и
// во всех строках сразу.
type managedTPColumn struct {
	Field  metadata.Field
	Hidden bool
	// Index — позиция реквизита в метаданных табличной части, а НЕ в порядке
	// показа. Именно её сверяет сервер (canonicalTPColumn) со служебным полем
	// _tp_col_index. Пока порядок показа совпадал с метаданными, клиент считал
	// индекс сам по видимым колонкам; с выбором состава эти порядки разошлись.
	Index   int
	Element *metadata.FormElement // элемент kind: Колонка, если объявлен
}

// managedTPColumnPlan раскладывает реквизиты табличной части в порядок показа,
// заданный детьми kind: Колонка у элемента формы. Дети задают и состав, и
// порядок; невыбранные реквизиты уходят в конец плана скрытыми.
//
// Ни одной разрешённой колонки-ребёнка — план равен метаданным. Так выглядят
// все конфигурации, написанные до появления выбора состава, и так же выглядит
// табличная часть, которую в конструкторе не трогали: «ничего не выбрано»
// значит «показать всё», а не «показать пусто».
func managedTPColumnPlan(el *metadata.FormElement, fields []metadata.Field) []managedTPColumn {
	full := func() []managedTPColumn {
		plan := make([]managedTPColumn, 0, len(fields))
		for index, field := range fields {
			plan = append(plan, managedTPColumn{Field: field, Index: index})
		}
		return plan
	}
	if el == nil {
		return full()
	}

	chosen := make(map[int]*metadata.FormElement, len(fields))
	order := make([]int, 0, len(fields))
	for _, child := range el.Children {
		if child == nil || child.Kind != metadata.FormElementColumn {
			continue
		}
		index, ok := managedTPFieldIndexForColumn(fields, child)
		if !ok {
			continue
		}
		// Один реквизит, размещённый колонкой дважды, показывается один раз:
		// две ячейки с одинаковым name= в одной строке дали бы неоднозначный
		// payload, а какая из них победит — зависело бы от порядка разбора.
		if _, duplicate := chosen[index]; duplicate {
			continue
		}
		chosen[index] = child
		order = append(order, index)
	}
	if len(order) == 0 {
		return full()
	}

	plan := make([]managedTPColumn, 0, len(fields))
	for _, index := range order {
		plan = append(plan, managedTPColumn{Field: fields[index], Index: index, Element: chosen[index]})
	}
	for index, field := range fields {
		if _, shown := chosen[index]; shown {
			continue
		}
		plan = append(plan, managedTPColumn{Field: field, Index: index, Hidden: true})
	}
	return plan
}

// managedTPFieldIndexForColumn сопоставляет элемент kind: Колонка реквизиту
// табличной части. Порядок источников — от самого явного к самому короткому:
// data_path (его пишет конструктор), field, имя элемента.
func managedTPFieldIndexForColumn(fields []metadata.Field, column *metadata.FormElement) (int, bool) {
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
		for index := range fields {
			if strings.EqualFold(fields[index].Name, candidate) {
				return index, true
			}
		}
	}
	return 0, false
}

// managedTPColumnEvents — карта «имя реквизита → имя элемента-колонки» для
// колонок с объявленным обработчиком ПриИзменении. Клиент по ней решает, чей
// обработчик дёрнуть при правке ячейки; имя элемента (а не процедуры) — потому
// что резолвинг обработчика остаётся серверным и fail-closed.
func managedTPColumnEvents(plan []managedTPColumn, readOnly bool) map[string]string {
	if readOnly {
		return nil
	}
	events := make(map[string]string)
	for _, column := range plan {
		if column.Hidden || column.Element == nil {
			continue
		}
		if strings.TrimSpace(column.Element.Handlers[metadata.FormEventOnChange]) == "" {
			continue
		}
		if strings.TrimSpace(column.Element.Name) == "" {
			continue
		}
		events[column.Field.Name] = column.Element.Name
	}
	if len(events) == 0 {
		return nil
	}
	return events
}
