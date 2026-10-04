package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// Проверки размещения ключей элемента формы, у которых «ключ принят, но ничего
// не делает» — это молчаливое расхождение конфигурации с интерфейсом. Разделение
// по строгости намеренное: запрет редактирования обязан быть ошибкой, потому что
// иначе конфигурация обещает недоступность, которой нет; остальные четыре ключа
// в неподходящем месте лишь бесполезны и остаются предупреждением.

// CheckFormAdminOnly — editable_admin_only живёт только на элементе-поле с
// двухсегментным data_path Объект.<Реквизит>. Именно такое поле сервер отбрасывает из присланной
// записи; на колонке табличной части или на элементе без data_path запрет
// нарисовался бы, но не действовал.
func CheckFormAdminOnly(proj *project.Project) []Issue {
	if proj == nil {
		return nil
	}
	var issues []Issue
	forEachProjectForm(proj, func(owner *metadata.Entity, form *metadata.FormModule) {
		walkFormElements(form.Elements, func(el *metadata.FormElement) {
			if el == nil || !el.EditableAdminOnly {
				return
			}
			if el.Kind != metadata.FormElementField {
				issues = append(issues, formKeyIssue(owner, form, el, "form.admin-only",
					fmt.Sprintf("editable_admin_only допустим только у kind: %s", metadata.FormElementField),
					"Уберите ключ или перенесите его на поле ввода."))
				return
			}
			if !isTwoSegmentDataPath(el.DataPath) {
				issues = append(issues, formKeyIssue(owner, form, el, "form.admin-only",
					fmt.Sprintf("editable_admin_only требует data_path вида Объект.<Реквизит>, а задан %q", el.DataPath),
					"Колонку табличной части и элемент без data_path сервер не запирает — запрет был бы только в разметке."))
			}
		})
	})
	return issues
}

// CheckFormChoiceFolders requires the stable element id used by the picker
// context. Without it, applyManagedChoiceFilters skips the field entirely.
func CheckFormChoiceFolders(proj *project.Project) []Issue {
	if proj == nil {
		return nil
	}
	var issues []Issue
	forEachProjectForm(proj, func(owner *metadata.Entity, form *metadata.FormModule) {
		idCount := make(map[string]int)
		form.Walk(func(el *metadata.FormElement) bool {
			if el != nil && strings.TrimSpace(el.ID) != "" {
				idCount[strings.TrimSpace(el.ID)]++
			}
			return true
		})
		form.Walk(func(el *metadata.FormElement) bool {
			if el == nil || !el.ChoiceFolders || el.ChoiceFilter != nil {
				return true // choice_filter already checks this id
			}
			if id := strings.TrimSpace(el.ID); id == "" || id != el.ID || idCount[id] != 1 {
				issues = append(issues, formKeyIssue(owner, form, el, "form.choice-folders",
					"choice_folders требует непустой уникальный стабильный id элемента",
					"Задайте уникальный id, чтобы серверный контекст подбора применял ключ."))
			}
			return true
		})
	})
	return issues
}

// CheckFormKeyPlacement предупреждает о ключах, которые в этом месте рантайм не
// применит: scroll_x вне горизонтальной группы, primary вне кнопки,
// choice_dropdown и choice_folders вне ссылочного поля, choice_folders у
// неиерархического справочника.
func CheckFormKeyPlacement(proj *project.Project) []Issue {
	if proj == nil {
		return nil
	}
	entities := make(map[string]*metadata.Entity, len(proj.Entities))
	for _, entity := range proj.Entities {
		if entity != nil {
			entities[strings.ToLower(entity.Name)] = entity
		}
	}

	var warns []Issue
	forEachProjectForm(proj, func(owner *metadata.Entity, form *metadata.FormModule) {
		walkFormElements(form.Elements, func(el *metadata.FormElement) {
			if el == nil {
				return
			}
			if el.ScrollX && (el.Kind != metadata.FormElementGroupBox || !strings.EqualFold(el.Orientation, "horizontal")) {
				warns = append(warns, formKeyIssue(owner, form, el, "form.scroll-x",
					"ключ scroll_x игнорируется — прокрутка вместо переноса есть только у ГруппаФормы с orientation: horizontal",
					"Задайте orientation: horizontal у группы или уберите ключ."))
			}
			if el.Primary && el.Kind != metadata.FormElementButton {
				warns = append(warns, formKeyIssue(owner, form, el, "form.primary",
					fmt.Sprintf("ключ primary игнорируется — акцентный стиль есть только у kind: %s", metadata.FormElementButton),
					"Уберите ключ или перенесите его на кнопку."))
			}
			if !el.ChoiceFolders && el.ChoiceDropdown == nil {
				return
			}
			target, ok := formChoiceRefSource(owner, form, el.DataPath, entities)
			if el.Kind != metadata.FormElementField || !ok || target == nil || target.Kind != metadata.KindCatalog {
				warns = append(warns, formKeyIssue(owner, form, el, "form.choice-keys",
					fmt.Sprintf("ключи choice_folders/choice_dropdown игнорируются — data_path %q не выбирает ссылку на справочник", el.DataPath),
					"Оставьте ключ только у ссылочного поля ввода."))
				return
			}
			if el.ChoiceFolders && !target.Hierarchical {
				warns = append(warns, formKeyIssue(owner, form, el, "form.choice-keys",
					fmt.Sprintf("ключ choice_folders ничего не меняет — у справочника %s нет иерархии, групп в подборе не бывает", target.Name),
					"Уберите ключ или включите иерархию справочника."))
			}
		})
	})
	return warns
}

func formKeyIssue(owner *metadata.Entity, form *metadata.FormModule, el *metadata.FormElement, code, message, fix string) Issue {
	object := ""
	if owner != nil {
		object = owner.Name
	}
	return Issue{
		File:         formFileLabel(owner, form),
		Object:       object,
		Kind:         "Управляемая форма",
		Code:         code,
		Message:      fmt.Sprintf("элемент %q: %s", formElementName(el), message),
		SuggestedFix: fix,
	}
}

func forEachProjectForm(proj *project.Project, fn func(owner *metadata.Entity, form *metadata.FormModule)) {
	for _, owner := range proj.Entities {
		if owner == nil {
			continue
		}
		for _, form := range owner.Forms {
			if form == nil {
				continue
			}
			fn(owner, form)
		}
	}
}

// isTwoSegmentDataPath — путь ровно из двух непустых сегментов с известным
// корнем Объект. Колонка табличной части («Объект.Строки.Цена»)
// и реквизит формы сюда не попадают.
func isTwoSegmentDataPath(path string) bool {
	parts := strings.Split(strings.TrimSpace(path), ".")
	if len(parts) != 2 {
		return false
	}
	root, name := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	if name == "" {
		return false
	}
	return strings.EqualFold(root, "Объект")
}
