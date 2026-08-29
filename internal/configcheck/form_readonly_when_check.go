package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckFormReadOnlyWhen возвращает БЛОКИРУЮЩИЕ ошибки для readonly_when на
// контейнерах, для которых условный запрет не имеет однозначной семантики.
// Условие действует только на сам элемент и не наследуется детьми, поэтому
// разрешить его на группе или наборе страниц означало бы принять конфигурацию,
// которая выглядит как запрет всей области, но оставляет дочерние поля
// редактируемыми.
func CheckFormReadOnlyWhen(proj *project.Project) []Issue {
	if proj == nil {
		return nil
	}

	var issues []Issue
	report := func(label, object string, form *metadata.FormModule) {
		if form == nil {
			return
		}
		walkFormElements(form.Elements, func(el *metadata.FormElement) {
			if strings.TrimSpace(el.ReadOnlyWhen) == "" || !formReadOnlyWhenUnsupportedContainer(el.Kind) {
				return
			}
			issues = append(issues, Issue{
				File:         label,
				Object:       object,
				Kind:         "Управляемая форма",
				Code:         "form.readonly-when-container",
				Message:      fmt.Sprintf("элемент %q (kind: %s) задаёт readonly_when, но условная нередактируемость контейнера не распространяется на дочерние элементы", formElementName(el), el.Kind),
				SuggestedFix: "Перенесите readonly_when на конкретные редактируемые элементы внутри контейнера; для условного скрытия всего контейнера используйте hidden_when.",
			})
		})
	}

	for _, ent := range proj.Entities {
		if ent == nil {
			continue
		}
		for _, form := range ent.Forms {
			if form == nil {
				continue
			}
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
	return issues
}

func formReadOnlyWhenUnsupportedContainer(kind metadata.FormElementType) bool {
	switch kind {
	case metadata.FormElementGroupBox, metadata.FormElementPages, metadata.FormElementPage:
		return true
	default:
		return false
	}
}
