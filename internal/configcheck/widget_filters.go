package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/widget"
)

// План 182D: интерактивные фильтры list-виджета. Контракт строгий на этапе
// check: анонсированный в YAML фильтр, который потом молча не работает
// (неизвестный тип, параметр не встречается в запросе, конфликт со статикой) —
// конфигурационный дефект, а не особенность рантайма.

// CheckWidgetFilters validates the interactive filter declarations of list
// widgets.
func CheckWidgetFilters(proj *project.Project) []Issue {
	var issues []Issue
	for _, w := range proj.Widgets {
		if len(w.Filters) == 0 {
			continue
		}
		if w.Type != metadata.WidgetTypeList {
			issues = append(issues, Issue{
				File:         "widgets/" + w.Name + ".yaml",
				Object:       w.Name,
				Kind:         "Виджет",
				Message:      "filters поддерживаются только для виджетов типа list",
				SuggestedFix: "Уберите filters или смените тип виджета",
			})
			continue
		}
		seenName := map[string]bool{}
		seenParam := map[string]bool{}
		for _, f := range w.Filters {
			if strings.TrimSpace(f.Name) == "" || strings.TrimSpace(f.Param) == "" {
				issues = append(issues, Issue{
					File:    "widgets/" + w.Name + ".yaml",
					Object:  w.Name,
					Kind:    "Виджет (filters)",
					Message: "каждый фильтр требует заполненные name и param",
				})
				continue
			}
			if seenName[strings.ToLower(f.Name)] {
				issues = append(issues, Issue{
					File:    "widgets/" + w.Name + ".yaml",
					Object:  w.Name,
					Kind:    "Виджет (filters)",
					Message: fmt.Sprintf("имя фильтра %q повторяется", f.Name),
				})
			}
			seenName[strings.ToLower(f.Name)] = true
			if seenParam[strings.ToLower(f.Param)] {
				issues = append(issues, Issue{
					File:    "widgets/" + w.Name + ".yaml",
					Object:  w.Name,
					Kind:    "Виджет (filters)",
					Message: fmt.Sprintf("param %q используется двумя фильтрами", f.Param),
				})
			}
			seenParam[strings.ToLower(f.Param)] = true
			if _, collides := w.Params[f.Param]; collides {
				issues = append(issues, Issue{
					File:         "widgets/" + w.Name + ".yaml",
					Object:       w.Name,
					Kind:         "Виджет (filters)",
					Message:      fmt.Sprintf("параметр фильтра %q совпадает со статическим params — интерактивное значение будет теряться", f.Param),
					SuggestedFix: "Переименуйте param фильтра",
				})
			}
			if !widget.IsKnownFilterType(f.Type) {
				issues = append(issues, Issue{
					File:         "widgets/" + w.Name + ".yaml",
					Object:       w.Name,
					Kind:         "Виджет (filters)",
					Message:      fmt.Sprintf("неизвестный тип фильтра %q", f.Type),
					SuggestedFix: "string | number | date | bool | select | reference:<Сущность>",
				})
				continue
			}
			if f.Type == "select" && len(f.Values) == 0 {
				issues = append(issues, Issue{
					File:    "widgets/" + w.Name + ".yaml",
					Object:  w.Name,
					Kind:    "Виджет (filters)",
					Message: fmt.Sprintf("фильтр %q типа select требует непустой values", f.Name),
				})
			}
			if entity := f.ReferenceEntity(); entity != "" {
				found := false
				for _, e := range proj.Entities {
					if strings.EqualFold(e.Name, entity) {
						found = true
						break
					}
				}
				if !found {
					issues = append(issues, Issue{
						File:    "widgets/" + w.Name + ".yaml",
						Object:  w.Name,
						Kind:    "Виджет (filters)",
						Message: fmt.Sprintf("сущность %q фильтра %q не найдена в конфигурации", entity, f.Name),
					})
				}
			}
			// param обязан встречаться в тексте запроса: DSL регистронезависим,
			// ищем &Параметр без учёта регистра.
			if !strings.Contains(strings.ToLower(w.Query), "&"+strings.ToLower(f.Param)) {
				issues = append(issues, Issue{
					File:         "widgets/" + w.Name + ".yaml",
					Object:       w.Name,
					Kind:         "Виджет (filters)",
					Message:      fmt.Sprintf("параметр фильтра %q не встречается в запросе (&%s)", f.Param, f.Param),
					SuggestedFix: fmt.Sprintf("Добавьте в запрос условие с &%s (например, «&%s ЕСТЬ ПУСТО ИЛИ Поле = &%s»)", f.Param, f.Param, f.Param),
				})
			}
			if err := checkFilterDefault(f); err != nil {
				issues = append(issues, Issue{
					File:    "widgets/" + w.Name + ".yaml",
					Object:  w.Name,
					Kind:    "Виджет (filters)",
					Message: err.Error(),
				})
			}
		}
	}
	return issues
}

// checkFilterDefault проверяет умолчание фильтра тем же парсером, что и
// рантайм: подходит — пройдёт и в URL.
func checkFilterDefault(f metadata.WidgetFilter) error {
	if f.Default == nil {
		return nil
	}
	if f.Type == "bool" {
		if b, ok := f.Default.(bool); ok {
			if b {
				return widget.ValidateFilterSample(f, "true")
			}
			return widget.ValidateFilterSample(f, "false")
		}
		return fmt.Errorf("умолчание фильтра %q должно быть булевым", f.Name)
	}
	return widget.ValidateFilterSample(f, fmt.Sprintf("%v", f.Default))
}
