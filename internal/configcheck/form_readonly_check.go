package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckFormReadonlyConflict предупреждает только о собственном постоянном
// запрете вместе с условным на одном элементе. Унаследованный readonly сюда
// не входит: условие потомка может быть нужно при переносе в другую группу.
func CheckFormReadonlyConflict(proj *project.Project) []Issue {
	if proj == nil {
		return nil
	}
	var warnings []Issue
	report := func(label, object string, form *metadata.FormModule) {
		if form == nil {
			return
		}
		walkFormElements(form.Elements, func(el *metadata.FormElement) {
			if !el.ReadOnly || strings.TrimSpace(el.ReadOnlyWhen) == "" {
				return
			}
			warnings = append(warnings, Issue{
				File:         label,
				Object:       object,
				Kind:         "Управляемая форма",
				Code:         "form.readonly-conflict",
				Message:      fmt.Sprintf("элемент %q: readonly: true имеет приоритет над readonly_when; ложное условие не снимает постоянный запрет редактирования", formElementName(el)),
				SuggestedFix: "Для условного запрета уберите readonly: true; для постоянного запрета уберите избыточный readonly_when.",
			})
		})
	}
	for _, ent := range proj.Entities {
		if ent == nil {
			continue
		}
		for _, form := range ent.Forms {
			report(formFileLabel(ent, form), ent.Name, form)
		}
	}
	for _, proc := range proj.Processors {
		if proc == nil {
			continue
		}
		for _, form := range proc.Forms {
			report(procFormFileLabel(proc.Name, form), proc.Name, form)
		}
	}
	return warnings
}
