package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/project"
)

// knownEntityFormKinds — виды управляемой формы справочника/документа, которые
// движок выбирает: object (карточка), list, choice; folder и custom знает
// конструктор, custom же получает форма без kind (её открывают по имени).
var knownEntityFormKinds = map[string]bool{
	"object": true, "list": true, "choice": true, "folder": true, "custom": true,
}

// CheckFormHeaderKind — ошибка на неизвестный form.kind формы справочника или
// документа. Такая форма молча не подбиралась ни для карточки, ни для списка:
// kind: document уводил форму оценки звонка в автоформу без единого слова.
// Формы обработок kind не используют (берётся первая управляемая форма),
// поэтому не проверяются.
func CheckFormHeaderKind(proj *project.Project) []Issue {
	if proj == nil {
		return nil
	}
	var issues []Issue
	for _, ent := range proj.Entities {
		if ent == nil {
			continue
		}
		for _, form := range ent.Forms {
			if form == nil || !form.IsManaged() {
				continue
			}
			kind := strings.ToLower(strings.TrimSpace(form.Kind))
			if knownEntityFormKinds[kind] {
				continue
			}
			issues = append(issues, Issue{
				File:         formFileLabel(ent, form),
				Object:       ent.Name,
				Kind:         "Управляемая форма",
				Code:         "form.header-kind",
				Message:      fmt.Sprintf("form.kind %q не известен: форма не будет выбрана ни для карточки, ни для списка — откроется автоформа", form.Kind),
				SuggestedFix: "Укажите kind: object (карточка), list (список), choice (выбор), folder (группа) или custom (открывается по имени).",
			})
		}
	}
	return issues
}
