package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/csssafe"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckFormBackground предупреждает о фоне элемента, который рантайм не
// применит. Ключ background читают ГруппаФормы (#1547) и Кнопка, значение
// обязано проходить csssafe.Color. Класс тот же, что у раскладки (#1185):
// ключ принимался без диагностики — значит, расхождение обязан называть
// check, а не наблюдение «почему-то не позеленело».

func CheckFormBackground(proj *project.Project) []Issue {
	if proj == nil {
		return nil
	}

	var warns []Issue
	report := func(label, object string, form *metadata.FormModule) {
		if form == nil {
			return
		}
		walkFormElements(form.Elements, func(el *metadata.FormElement) {
			for _, hk := range []struct{ key, value string }{
				{"header_background", el.HeaderBackground},
				{"header_border_color", el.HeaderBorderColor},
			} {
				if strings.TrimSpace(hk.value) == "" {
					continue
				}
				if el.Kind != metadata.FormElementTablePart {
					warns = append(warns, Issue{
						File:         label,
						Object:       object,
						Kind:         "Управляемая форма",
						Code:         "form.background-kind",
						Message:      fmt.Sprintf("элемент %q (%s): ключ %s игнорируется — цвет шапки есть только у ТабличнаяЧасть", formElementName(el), el.Kind, hk.key),
						SuggestedFix: "Удалите ключ или перенесите его на ТабличнаяЧасть.",
					})
				} else if csssafe.Color(hk.value) == "" {
					warns = append(warns, Issue{
						File:         label,
						Object:       object,
						Kind:         "Управляемая форма",
						Code:         "form.background-color",
						Message:      fmt.Sprintf("%s %q: %s %q — не распознанный цвет, шапка останется обычной", el.Kind, formElementName(el), hk.key, hk.value),
						SuggestedFix: "Допустимы hex (#rrggbb, #rgb), rgb(...) и именованные CSS-цвета.",
					})
				}
			}
			if strings.TrimSpace(el.Background) == "" {
				return
			}
			if !metadata.FormElementSupportsBackground(el.Kind) {
				warns = append(warns, Issue{
					File:         label,
					Object:       object,
					Kind:         "Управляемая форма",
					Code:         "form.background-kind",
					Message:      fmt.Sprintf("элемент %q (%s): ключ background игнорируется — фон поддерживают только ГруппаФормы и Кнопка", formElementName(el), el.Kind),
					SuggestedFix: "Перенесите background на объемлющую ГруппаФормы или удалите ключ.",
				})
				return
			}
			if csssafe.Color(el.Background) == "" {
				warns = append(warns, Issue{
					File:         label,
					Object:       object,
					Kind:         "Управляемая форма",
					Code:         "form.background-color",
					Message:      fmt.Sprintf("%s %q: background %q — не распознанный цвет, фон применён не будет", el.Kind, formElementName(el), el.Background),
					SuggestedFix: "Допустимы hex (#rrggbb, #rgb), rgb(...) и именованные CSS-цвета.",
				})
			}
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
			if form == nil {
				continue
			}
			name := form.Name
			if name == "" {
				name = "объекта"
			}
			report("forms/"+strings.ToLower(proc.Name)+"/"+name+".form.yaml", proc.Name, form)
		}
	}
	return warns
}
