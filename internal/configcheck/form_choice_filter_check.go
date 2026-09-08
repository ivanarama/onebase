package configcheck

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckFormChoiceFilter проверяет связи параметров выбора (`choice_filter`) на
// формах: «реквизит выбираемого справочника → путь к значению на форме».
//
// Проверка БЛОКИРУЮЩАЯ, и это не строгость ради строгости. Опечатка в имени
// реквизита не видна ничем: отбор просто не применяется, подбор показывает весь
// справочник, и это неотличимо от «связь ещё не настроили». Ошибка же при сборке
// показывает и файл, и поле.
//
// Что проверяем:
//   - поле с choice_filter само ссылочное (отбирать нечего у строки или числа);
//   - реквизит-приёмник существует у справочника, на который поле ссылается;
//   - источник значения существует на форме: реквизит объекта, реквизит формы
//     или поле табличной части.
func CheckFormChoiceFilter(proj *project.Project) []Issue {
	if proj == nil {
		return nil
	}
	entities := map[string]*metadata.Entity{}
	for _, e := range proj.Entities {
		entities[strings.ToLower(e.Name)] = e
	}
	var issues []Issue
	for _, ent := range proj.Entities {
		for _, form := range ent.Forms {
			label := formFileLabel(ent, form)
			add := func(msg, fix string) {
				issues = append(issues, Issue{
					File:         label,
					Object:       ent.Name,
					Kind:         "Управляемая форма",
					Code:         "form.choice-filter",
					Message:      msg,
					SuggestedFix: fix,
				})
			}
			form.Walk(func(el *metadata.FormElement) bool {
				if el == nil || len(el.ChoiceFilter) == 0 {
					return true
				}
				name := formElementName(el)
				target := choiceFilterTargetEntity(entities, ent, form, el)
				if target == nil {
					add(fmt.Sprintf("поле %q задаёт choice_filter, но не ссылается на справочник — отбирать нечего", name),
						"Уберите choice_filter или свяжите поле со справочником (data_path на ссылочный реквизит).")
					return true
				}
				// Порядок ключей карты в Go случайный: сортируем, чтобы одна и та
				// же конфигурация давала один и тот же список ошибок.
				keys := make([]string, 0, len(el.ChoiceFilter))
				for k := range el.ChoiceFilter {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, key := range keys {
					if !entityHasFieldFold(target, key) {
						add(fmt.Sprintf("поле %q: choice_filter отбирает по реквизиту %q, которого нет у справочника %s",
							name, key, target.Name),
							fmt.Sprintf("Укажите существующий реквизит %s (у подчинённого справочника это «%s»).",
								target.Name, metadata.StandardOwnerField))
						continue
					}
					src := formDataPathField(el.ChoiceFilter[key])
					if src == "" {
						add(fmt.Sprintf("поле %q: choice_filter для %q не задаёт источник значения", name, key),
							"Укажите путь к значению: choice_filter: { "+key+": Объект.Контрагент }.")
						continue
					}
					if !formHasValueSource(ent, form, src) {
						add(fmt.Sprintf("поле %q: choice_filter берёт значение из %q, но такого реквизита нет ни у %s, ни у формы",
							name, src, ent.Name),
							"Проверьте написание: источником бывает реквизит объекта (Объект.Контрагент) или реквизит формы.")
					}
				}
				return true
			})
		}
	}
	return issues
}

// choiceFilterTargetEntity — справочник, элементы которого выбирает поле: либо
// по реквизиту объекта, либо по реквизиту формы (save:false).
func choiceFilterTargetEntity(entities map[string]*metadata.Entity, ent *metadata.Entity, form *metadata.FormModule, el *metadata.FormElement) *metadata.Entity {
	field := formDataPathField(el.DataPath)
	if field == "" {
		return nil
	}
	if ent != nil {
		for _, f := range ent.Fields {
			if strings.EqualFold(f.Name, field) && f.RefEntity != "" {
				return entities[strings.ToLower(f.RefEntity)]
			}
		}
	}
	if form != nil {
		for _, a := range form.Attributes {
			if a == nil || !strings.EqualFold(a.Name, field) {
				continue
			}
			ref := strings.TrimPrefix(a.TypeRef, "CatalogRef.")
			ref = strings.TrimPrefix(ref, "DocumentRef.")
			if ref == a.TypeRef {
				return nil
			}
			return entities[strings.ToLower(ref)]
		}
	}
	return nil
}

// formHasValueSource — есть ли на форме источник значения с таким именем.
func formHasValueSource(ent *metadata.Entity, form *metadata.FormModule, name string) bool {
	if entityHasFieldFold(ent, name) {
		return true
	}
	if ent != nil {
		for _, tp := range ent.TableParts {
			for _, f := range tp.Fields {
				if strings.EqualFold(f.Name, name) {
					return true
				}
			}
		}
	}
	if form != nil {
		for _, a := range form.Attributes {
			if a != nil && strings.EqualFold(a.Name, name) {
				return true
			}
		}
	}
	return false
}

func entityHasFieldFold(ent *metadata.Entity, name string) bool {
	if ent == nil {
		return false
	}
	for _, f := range ent.Fields {
		if strings.EqualFold(f.Name, name) {
			return true
		}
	}
	return false
}
