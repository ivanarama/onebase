package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckFormElementKind возвращает БЛОКИРУЮЩИЕ ошибки об элементах формы с
// неизвестным `kind`. yaml.v3 кладёт любую строку в FormElement.Kind как есть,
// поэтому выдуманный вид (напр. «ПолеИзображения», «ПолеФайла») раньше проходил
// check молча, а в рантайме форма показывала «рендеринг не реализован» — из-за
// чего каркас-ассистент, видя «проверка OK», ходил по кругу (issue #598).
func CheckFormElementKind(proj *project.Project) []Issue {
	var issues []Issue
	known := formKindList()
	report := func(label, object string, form *metadata.FormModule) {
		walkFormElements(form.Elements, func(el *metadata.FormElement) {
			if metadata.IsKnownFormElementType(el.Kind) {
				return
			}
			issues = append(issues, Issue{
				File:    label,
				Object:  object,
				Kind:    "Управляемая форма",
				Code:    "form.unknown-kind",
				Message: fmt.Sprintf("элемент %q задаёт неизвестный вид kind: %s", formElementName(el), el.Kind),
				SuggestedFix: "Используйте поддерживаемый вид: " + known + ". Для картинки в поле типа image — kind: ПолеВвода (не ПолеФайла/ПолеИзображения).",
			})
		})
	}
	for _, ent := range proj.Entities {
		for _, form := range ent.Forms {
			report(formFileLabel(ent, form), ent.Name, form)
		}
	}
	for _, p := range proj.Processors {
		for _, form := range p.Forms {
			name := form.Name
			if name == "" {
				name = "объекта"
			}
			label := "forms/" + strings.ToLower(p.Name) + "/" + name + ".form.yaml"
			report(label, p.Name, form)
		}
	}
	return issues
}

// formKindList — человекочитаемый перечень поддерживаемых видов элементов формы
// для сообщения об ошибке.
func formKindList() string {
	kinds := metadata.KnownFormElementTypes()
	parts := make([]string, len(kinds))
	for i, k := range kinds {
		parts[i] = string(k)
	}
	return strings.Join(parts, ", ")
}
