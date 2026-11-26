package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/csssafe"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckFormBackground проверяет фон элементов и размещение оформления подписей и закладок.
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
			for _, style := range []struct {
				key  string
				set  bool
				kind metadata.FormElementType
			}{
				{"label_color", strings.TrimSpace(el.LabelColor) != "", metadata.FormElementField},
				{"title_color", strings.TrimSpace(el.TitleColor) != "", metadata.FormElementPage},
				{"title_bold", el.TitleBold, metadata.FormElementPage},
				{"tab_titles_bold", el.TabTitlesBold, metadata.FormElementPages},
			} {
				if style.set && el.Kind != style.kind {
					warns = append(warns, Issue{
						File: label, Object: object, Kind: "Управляемая форма", Code: "form.style-kind",
						Message:      fmt.Sprintf("элемент %q: ключ %s игнорируется у %s", formElementName(el), style.key, el.Kind),
						SuggestedFix: fmt.Sprintf("Перенесите ключ на элемент вида %s или удалите его.", style.kind),
					})
				}
			}
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
					Message:      fmt.Sprintf("элемент %q (%s): ключ background игнорируется — фон поддерживают только ГруппаФормы, СтраницыФормы и Кнопка", formElementName(el), el.Kind),
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
