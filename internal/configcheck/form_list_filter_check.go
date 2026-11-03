package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// listFilterOps — операторы постоянного отбора формы списка. Значение условия —
// одна строка, поэтому списочные in/not_in здесь не выражаются.
var listFilterOps = map[string]bool{"": true, "eq": true, "ne": true, "empty": true, "not_empty": true}

// CheckFormListFilters проверяет постоянный отбор формы списка (form.filter):
// поле — реквизит сущности (или одно разыменование ссылки до реквизита
// связанной сущности), оператор — из поддерживаемых. Ошибка, а не
// предупреждение: неизвестное поле или оператор проходят загрузку, но список
// падает на сервере (HTTP 500) при первом открытии.
func CheckFormListFilters(proj *project.Project) []Issue {
	var issues []Issue
	byName := make(map[string]*metadata.Entity, len(proj.Entities))
	for _, ent := range proj.Entities {
		byName[strings.ToLower(ent.Name)] = ent
	}
	for _, ent := range proj.Entities {
		for _, form := range ent.Forms {
			if form == nil {
				continue
			}
			for i, cond := range form.ListFilter {
				add := func(msg, fix string) {
					issues = append(issues, Issue{
						File: formFileLabel(ent, form), Object: ent.Name, Kind: "Управляемая форма",
						Code:         "form.list-filter",
						Message:      fmt.Sprintf("filter[%d]: %s", i, msg),
						SuggestedFix: fix,
					})
				}
				if op := strings.ToLower(strings.TrimSpace(cond.Op)); !listFilterOps[op] {
					add(fmt.Sprintf("оператор %q не поддерживается", cond.Op), "допустимы eq (по умолчанию), ne, empty, not_empty")
				}
				field := strings.TrimSpace(cond.Field)
				if field == "" {
					// Условие без поля исполнение пропускает (listFilterPredicate);
					// его же используют условия другого вида (отбор запросом).
					continue
				}
				head, attr, nested := strings.Cut(field, ".")
				if !nested {
					if !storage.PredicateFieldKnown(ent, head) {
						add(fmt.Sprintf("поле %q не найдено у %s", head, ent.Name), "укажите реквизит сущности")
					}
					continue
				}
				if strings.Contains(attr, ".") {
					add(fmt.Sprintf("поле %q: допустимо одно разыменование", field), "оставьте Ссылка.Реквизит")
					continue
				}
				var ref *metadata.Field
				for j := range ent.Fields {
					if strings.EqualFold(ent.Fields[j].Name, head) {
						ref = &ent.Fields[j]
						break
					}
				}
				if ref == nil || ref.RefEntity == "" {
					add(fmt.Sprintf("поле %q: %q не ссылочный реквизит %s", field, head, ent.Name), "разыменовывать можно только ссылку")
					continue
				}
				if strings.HasPrefix(ref.RefEntity, "_") {
					if !strings.EqualFold(ref.RefEntity, metadata.SystemUsersEntity) || !strings.EqualFold(attr, "id") {
						add(fmt.Sprintf("поле %q: системное разыменование %q не поддерживается", field, ref.RefEntity),
							"для reference:_users допустимо только Ссылка.id; остальные системные атрибуты недоступны")
					}
					continue
				}
				target := byName[strings.ToLower(ref.RefEntity)]
				if target == nil {
					add(fmt.Sprintf("поле %q: сущность %q не найдена", field, ref.RefEntity), "проверьте тип ссылки")
					continue
				}
				if !storage.PredicateFieldKnown(target, attr) {
					add(fmt.Sprintf("поле %q: реквизит %q не найден у %s", field, attr, target.Name), "укажите реквизит связанной сущности")
				}
			}
		}
	}
	return issues
}
