package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// План 182B: верхнеуровневый refresh_on виджета подписывает карточку на уже
// существующие события шины. Штатный издатель — «данные.<lower(имя)>» от
// сущности с notify_changes; объявленное имя живёт в браузере как точная
// строка, поэтому регистр и существование издателя проверяются заранее —
// молчаливо неоживающая подписка хуже явного предупреждения.

// refreshOnPublisherEvent — префикс штатных событий записи платформы.
const refreshOnPublisherEvent = "данные."

// CheckWidgetRefreshOn reports hard errors of the widget refresh_on contract:
// у actions нет результата запроса, который можно перечитать.
func CheckWidgetRefreshOn(proj *project.Project) []Issue {
	var issues []Issue
	for _, w := range proj.Widgets {
		if w.Type == metadata.WidgetTypeActions && len(w.RefreshOn) > 0 {
			issues = append(issues, Issue{
				File:         "widgets/" + w.Name + ".yaml",
				Object:       w.Name,
				Kind:         "Виджет",
				Message:      "refresh_on не поддерживается для виджетов типа actions: у них нет результата запроса, который можно перечитать",
				SuggestedFix: "Уберите refresh_on или смените тип виджета",
			})
		}
	}
	return issues
}

// CheckWidgetRefreshOnPublisherWarnings warns about refresh_on записи вида
// «данные.<сущность>», у которой издателя не будет: сущности нет, имя не
// совпадает по регистру с публикуемым или у сущности не включён notify_changes.
func CheckWidgetRefreshOnPublisherWarnings(proj *project.Project) []Issue {
	byLower := make(map[string]*metadata.Entity, len(proj.Entities))
	for _, e := range proj.Entities {
		byLower[strings.ToLower(e.Name)] = e
	}
	var warnings []Issue
	for _, w := range proj.Widgets {
		for _, ev := range w.RefreshOn {
			if !strings.HasPrefix(ev, refreshOnPublisherEvent) {
				continue // не штатное событие записи: его имя шлёт конфигурация сама
			}
			declared := strings.TrimPrefix(ev, refreshOnPublisherEvent)
			entity, ok := byLower[strings.ToLower(declared)]
			if !ok {
				warnings = append(warnings, Issue{
					File:    "widgets/" + w.Name + ".yaml",
					Object:  w.Name,
					Kind:    "Виджет (refresh_on)",
					Message: fmt.Sprintf("событие %s: сущность %q не найдена — событие никто не опубликует", ev, declared),
				})
				continue
			}
			published := strings.ToLower(entity.Name)
			if declared != published {
				warnings = append(warnings, Issue{
					File:         "widgets/" + w.Name + ".yaml",
					Object:       w.Name,
					Kind:         "Виджет (refresh_on)",
					Message:      fmt.Sprintf("событие %s: публикуется %s%s — подписка по строке не совпадёт", ev, refreshOnPublisherEvent, published),
					SuggestedFix: "Замените на " + refreshOnPublisherEvent + published,
				})
				continue
			}
			if !entity.NotifyChanges {
				warnings = append(warnings, Issue{
					File:         "widgets/" + w.Name + ".yaml",
					Object:       w.Name,
					Kind:         "Виджет (refresh_on)",
					Message:      fmt.Sprintf("событие %s: у сущности %q не включён notify_changes, событие записи публиковаться не будет", ev, entity.Name),
					SuggestedFix: fmt.Sprintf("Включите notify_changes: true в описании сущности %q", entity.Name),
				})
			}
		}
	}
	return warnings
}
