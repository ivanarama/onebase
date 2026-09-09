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

// managedColumnField переносит явный заголовок элемента формы в копию
// реквизита. Идентификатор и остальные свойства реквизита не меняются, поэтому
// один и тот же результат безопасно используют и таблица формы объекта, и
// список сущности.
//
// Порядок выбора совпадает с остальными managed-элементами: перевод для языка
// интерфейса, ru-fallback, legacy Title элемента, затем DisplayName реквизита.
func managedColumnField(field metadata.Field, element *metadata.FormElement) metadata.Field {
	if element == nil {
		return field
	}

	elementTitles := make(map[string]string, len(element.TitleMap))
	for lang, title := range element.TitleMap {
		if title != "" {
			elementTitles[lang] = title
		}
	}
	fallback := elementTitles["ru"]
	if fallback == "" {
		fallback = element.Title
	}
	if len(elementTitles) == 0 && fallback == "" {
		return field
	}

	result := field
	if fallback != "" {
		// Не оставляем локали реквизита: при явном ru/legacy-заголовке они не
		// должны оказаться старше fallback элемента.
		result.Titles = elementTitles
		result.Title = fallback
		return result
	}

	// У элемента есть только отдельные переводы без ru/legacy fallback.
	// Для остальных языков сохраняем обычный DisplayName реквизита.
	merged := make(map[string]string, len(field.Titles)+len(elementTitles))
	for lang, title := range field.Titles {
		merged[lang] = title
	}
	for lang, title := range elementTitles {
		merged[lang] = title
	}
	result.Titles = merged
	return result
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
		element := chosen[index]
		plan = append(plan, managedTPColumn{
			Field:   managedColumnField(fields[index], element),
			Index:   index,
			Element: element,
		})
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
