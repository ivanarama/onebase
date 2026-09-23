package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/query"
)

// План 182C: source объявляет навигацию строк list-виджета. Контракт строгий:
// клик открывает карточку только тогда, когда компилятор подтвердил, что
// колонка id_field — ссылка именно на объявленную сущность. Ошибку конфигурации
// ловим на check, чтобы автор не искал причину по «некликабельным» строкам.

// CheckWidgetSource validates the row-navigation declaration of list widgets.
func CheckWidgetSource(proj *project.Project) []Issue {
	var issues []Issue
	for _, w := range proj.Widgets {
		if w.Source == nil {
			continue
		}
		if w.Type != metadata.WidgetTypeList {
			issues = append(issues, Issue{
				File:         "widgets/" + w.Name + ".yaml",
				Object:       w.Name,
				Kind:         "Виджет",
				Message:      "source поддерживается только для виджетов типа list",
				SuggestedFix: "Уберите source или смените тип виджета",
			})
			continue
		}
		if strings.TrimSpace(w.Source.Entity) == "" || strings.TrimSpace(w.Source.IDField) == "" {
			issues = append(issues, Issue{
				File:    "widgets/" + w.Name + ".yaml",
				Object:  w.Name,
				Kind:    "Виджет",
				Message: "source требует заполненные entity и id_field",
			})
			continue
		}
		var entity *metadata.Entity
		for _, e := range proj.Entities {
			if strings.EqualFold(e.Name, w.Source.Entity) {
				entity = e
				break
			}
		}
		if entity == nil {
			issues = append(issues, Issue{
				File:    "widgets/" + w.Name + ".yaml",
				Object:  w.Name,
				Kind:    "Виджет (source)",
				Message: fmt.Sprintf("сущность %q не найдена в конфигурации", w.Source.Entity),
			})
			continue
		}
		compiled, err := query.Compile(w.Query, query.CompileOpts{
			Params:      paramsPlaceholder(w.Params),
			Entities:    proj.Entities,
			Registers:   proj.Registers,
			InfoRegs:    proj.InfoRegisters,
			AccountRegs: proj.AccountRegisters,
		})
		if err != nil {
			continue // ошибки компиляции уже репортит CheckQueries
		}
		// Кандидаты колонок — ключи RefColumns: ссылочная колонка результата
		// обязана среди них находиться. Реальный прогон не нужен, сопоставление
		// чисто компиляционное (тот же хелпер, что в рантайме виджета).
		cols := refColumnCandidates(&compiled)
		if query.ResolveRefOutputColumn(&compiled, w.Source.IDField, entity.Name, cols) == "" {
			issues = append(issues, Issue{
				File:         "widgets/" + w.Name + ".yaml",
				Object:       w.Name,
				Kind:         "Виджет (source)",
				Message:      fmt.Sprintf("колонка %q не подтверждена как ссылка на сущность %q: строки будут некликабельными", w.Source.IDField, entity.Name),
				SuggestedFix: fmt.Sprintf("Включите в запрос проекцию «Ссылка» (%s.Ссылка) и укажите её в id_field", entity.Name),
			})
		}
	}
	return issues
}

// refColumnCandidates возвращает имена колонок, подтверждённых как ссылки.
func refColumnCandidates(compiled *query.Result) []string {
	cols := make([]string, 0, len(compiled.RefColumns))
	for col := range compiled.RefColumns {
		cols = append(cols, col)
	}
	return cols
}

func paramsPlaceholder(params map[string]string) map[string]any {
	out := map[string]any{}
	for k := range params {
		out[k] = nil // placeholder so &Param doesn't fail name resolution
	}
	return out
}
