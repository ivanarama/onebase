package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckFormChoiceContext — границы choice_context (план 168): только ссылочное
// ПолеВвода, правая часть — только `Объект.<Реквизит>` той же сущности, без
// разыменования, реквизитов формы и выражений; имена параметров непустые.
// Нарушение иначе выглядело бы как «контекст не доехал до функции» — а это не
// отличить от «памятку не написали».
func CheckFormChoiceContext(proj *project.Project) []Issue {
	if proj == nil {
		return nil
	}

	var issues []Issue
	for _, owner := range proj.Entities {
		if owner == nil {
			continue
		}
		for _, form := range owner.Forms {
			if form == nil {
				continue
			}
			form.Walk(func(el *metadata.FormElement) bool {
				if el == nil || len(el.ChoiceContext) == 0 {
					return true
				}
				label := formFileLabel(owner, form)
				name := formElementName(el)
				add := func(format string, args ...any) {
					issues = append(issues, Issue{
						File:    label,
						Object:  owner.Name,
						Kind:    "Управляемая форма",
						Code:    "form.choice-context",
						Message: fmt.Sprintf("поле %q: %s", name, fmt.Sprintf(format, args...)),
					})
				}

				if el.Kind != metadata.FormElementField {
					add("choice_context допустим только у kind: %s", metadata.FormElementField)
				}
				if refEntity := choiceContextFieldRefEntity(owner, el.DataPath); refEntity == "" {
					add("data_path %q не выбирает ссылку; choice_context допустим только на ссылочном ПолеВвода", el.DataPath)
				}
				for alias, path := range el.ChoiceContext {
					if strings.TrimSpace(alias) == "" {
						add("пустое имя параметра context для пути %q", path)
					}
					src := strings.TrimSpace(path)
					lower := strings.ToLower(src)
					if !strings.HasPrefix(lower, "объект.") && !strings.HasPrefix(lower, "object.") {
						add("путь %q: правая часть choice_context должна быть Объект.<Реквизит> той же сущности, без разыменования и реквизитов формы", path)
						continue
					}
					fieldName := strings.TrimSpace(src[strings.Index(src, ".")+1:])
					if fieldName == "" || strings.Contains(fieldName, ".") {
						add("путь %q: ожидается Объект.<Реквизит> без разыменования", path)
						continue
					}
					if entityFieldByNameLoose(owner, fieldName) == nil {
						add("путь %q: реквизит %q не найден у сущности %s", path, fieldName, owner.Name)
					}
				}
				return true
			})
		}
	}
	return issues
}

// entityFieldByNameLoose — реквизит сущности без учёта регистра.
func entityFieldByNameLoose(entity *metadata.Entity, name string) *metadata.Field {
	if entity == nil {
		return nil
	}
	for i := range entity.Fields {
		if strings.EqualFold(entity.Fields[i].Name, strings.TrimSpace(name)) {
			return &entity.Fields[i]
		}
	}
	return nil
}

// choiceContextFieldRefEntity — сущность, на которую ссылается реквизит из
// data_path (`Объект.<Реквизит>`); пусто, если путь не ссылочный.
func choiceContextFieldRefEntity(owner *metadata.Entity, dataPath string) string {
	parts := strings.SplitN(strings.TrimSpace(dataPath), ".", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		return ""
	}
	field := entityFieldByNameLoose(owner, parts[1])
	if field == nil {
		return ""
	}
	return field.RefEntity
}
