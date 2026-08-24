package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckFormTablePartColumns проверяет объявления колонок табличной части
// (`kind: Колонка`) и события на них (план 154).
//
// Это ошибки, а не предупреждения, по той же причине, что и у виртуальных
// колонок: неверно объявленная колонка ничего не ломает — она просто молча не
// участвует в составе, а обработчик на ней молча не вызывается. Искать причину
// пришлось бы в браузере, а до тех пор форма выглядит настроенной.
func CheckFormTablePartColumns(proj *project.Project) []Issue {
	var issues []Issue
	report := func(label, object string, ent *metadata.Entity, form *metadata.FormModule) {
		walkFormElementsWithParent(form.Elements, nil, func(el, parent *metadata.FormElement) {
			if el.Kind != metadata.FormElementTablePart {
				return
			}
			add := func(msg, fix string) {
				issues = append(issues, Issue{
					File:         label,
					Object:       object,
					Kind:         "Управляемая форма",
					Code:         "form.column",
					Message:      fmt.Sprintf("элемент %q: %s", formElementName(el), msg),
					SuggestedFix: fix,
				})
			}
			checkTablePartColumns(add, ent, el)
		})
		// Колонка вне табличной части не попадает в состав ничего и потому
		// невидима вдвойне: её нет ни на форме, ни в диагностике.
		walkFormElementsWithParent(form.Elements, nil, func(el, parent *metadata.FormElement) {
			if el.Kind != metadata.FormElementColumn {
				return
			}
			if parent != nil && parent.Kind == metadata.FormElementTablePart {
				return
			}
			issues = append(issues, Issue{
				File:         label,
				Object:       object,
				Kind:         "Управляемая форма",
				Code:         "form.column",
				Message:      fmt.Sprintf("элемент %q: kind: Колонка объявлен вне табличной части", formElementName(el)),
				SuggestedFix: "вложите колонку в элемент kind: ТабличнаяЧасть — состав колонок задаётся только там",
			})
		})
	}

	for _, ent := range proj.Entities {
		for _, form := range ent.Forms {
			report(formFileLabel(ent, form), ent.Name, ent, form)
		}
	}
	for _, p := range proj.Processors {
		for _, form := range p.Forms {
			name := form.Name
			if name == "" {
				name = "объекта"
			}
			label := "forms/" + strings.ToLower(p.Name) + "/" + name + ".form.yaml"
			report(label, p.Name, nil, form)
		}
	}
	return issues
}

func checkTablePartColumns(add func(msg, fix string), ent *metadata.Entity, el *metadata.FormElement) {
	columns := make([]*metadata.FormElement, 0, len(el.Children))
	for _, child := range el.Children {
		if child != nil && child.Kind == metadata.FormElementColumn {
			columns = append(columns, child)
		}
	}
	if len(columns) == 0 {
		return
	}
	// Табличная часть формы обработки или ValueTable: состав её колонок живёт в
	// метаданных формы, сопоставлять не с чем — проверяем только события.
	tp := virtualColumnTablePart(ent, el)

	seen := make(map[string]*metadata.FormElement, len(columns))
	for _, column := range columns {
		name := formElementName(column)
		checkTablePartColumnEvents(add, el, column, name)
		if tp == nil {
			continue
		}
		field, ok := tablePartFieldForColumn(*tp, column)
		if !ok {
			add(fmt.Sprintf("колонка %q не сопоставлена реквизиту табличной части %q", name, tp.Name),
				"задайте data_path: Объект.<ТЧ>.<Реквизит> либо назовите элемент именем реквизита")
			continue
		}
		if previous, duplicate := seen[strings.ToLower(field)]; duplicate {
			add(fmt.Sprintf("колонки %q и %q ссылаются на один реквизит %q", formElementName(previous), name, field),
				"оставьте одно объявление: две ячейки одного реквизита в строке дали бы неоднозначный payload")
			continue
		}
		seen[strings.ToLower(field)] = column
	}
}

func checkTablePartColumnEvents(add func(msg, fix string), tablePart, column *metadata.FormElement, name string) {
	for event, proc := range column.Handlers {
		if strings.TrimSpace(proc) == "" {
			continue
		}
		if event != metadata.FormEventOnChange {
			add(fmt.Sprintf("колонка %q объявляет событие %q, которого колонка не отправляет", name, event),
				"колонка отправляет только ПриИзменении; события строки объявляйте на самой табличной части")
			continue
		}
		// no_grid возвращает простую таблицу, а событий ячейки в ней нет вовсе —
		// ни у колонки, ни у самой табличной части. Без этой проверки обработчик
		// выглядел бы настроенным и никогда не срабатывал.
		if tablePart.NoGrid {
			add(fmt.Sprintf("колонка %q объявляет ПриИзменении при no_grid: true", name),
				"события ячейки работают только в гриде: снимите no_grid у табличной части")
		}
	}
}

// tablePartFieldForColumn повторяет сопоставление рантайма (managedTPColumnPlan):
// data_path → field → имя элемента. Возвращает каноничное имя реквизита.
func tablePartFieldForColumn(tp metadata.TablePart, column *metadata.FormElement) (string, bool) {
	candidates := []string{
		lastFormPathSegment(column.DataPath),
		strings.TrimSpace(column.FieldName),
		strings.TrimSpace(column.Name),
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		for i := range tp.Fields {
			if strings.EqualFold(tp.Fields[i].Name, candidate) {
				return tp.Fields[i].Name, true
			}
		}
	}
	return "", false
}

func lastFormPathSegment(path string) string {
	trimmed := strings.TrimSpace(path)
	if index := strings.LastIndex(trimmed, "."); index >= 0 {
		return strings.TrimSpace(trimmed[index+1:])
	}
	return trimmed
}

func walkFormElementsWithParent(
	elements []*metadata.FormElement,
	parent *metadata.FormElement,
	visit func(el, parent *metadata.FormElement),
) {
	for _, el := range elements {
		if el == nil {
			continue
		}
		visit(el, parent)
		walkFormElementsWithParent(el.Children, el, visit)
	}
}
