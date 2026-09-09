package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/project"
)

// formAttrKnownTypes — типы реквизита управляемой формы, которые понимает
// загрузчик (docs/forms.md, раздел «Типы реквизитов»). Список сравнивается по
// ПРЕФИКСУ: «string(40)» и «decimal(15,2)» — те же типы с квалификаторами,
// «CatalogRef.Контрагент» — ссылка на конкретный справочник.
var formAttrKnownTypes = []string{
	"string", "number", "decimal", "date", "datetime", "bool",
	"catalogref.", "documentref.", "enumref.", "chartofaccountsref.", "anyref",
	"valuetable",
}

// CheckLintFormAttrTypes предупреждает о типе реквизита формы, которого
// загрузчик не знает.
//
// ПОЧЕМУ ЭТО ВООБЩЕ НУЖНО. Нераспознанный тип не ошибка и не пустое место:
// значение приходит в обработчик СТРОКОЙ. Реквизит с типом «Дата» (по-русски,
// как написано всё остальное в конфигурации) молча ведёт себя как строка —
// сравнение с датой не срабатывает, арифметика дат не работает, а на форме поле
// выглядит рабочим. Ссылочный тип в таком написании вдобавок теряет пикер:
// варианты выбора собираются только для CatalogRef./DocumentRef.
//
// ПРЕДУПРЕЖДЕНИЕ, А НЕ ОШИБКА. Незнакомый тип сегодня работает как строка, и
// конфигурации, где реквизит объявлен «Строка», этим пользуются — сломать их
// сборкой значило бы наказать за то, что платформа сама разрешала. Ошибкой это
// станет, если список типов когда-нибудь закроют.
func CheckLintFormAttrTypes(proj *project.Project) []Issue {
	if proj == nil {
		return nil
	}
	var issues []Issue
	for _, ent := range proj.Entities {
		for _, form := range ent.Forms {
			label := formFileLabel(ent, form)
			for _, a := range form.Attributes {
				if a == nil || strings.TrimSpace(a.TypeRef) == "" {
					continue
				}
				if formAttrTypeKnown(a.TypeRef) {
					continue
				}
				issues = append(issues, Issue{
					File:   label,
					Object: ent.Name,
					Kind:   "Управляемая форма",
					Code:   "form.attr-type",
					Message: fmt.Sprintf(
						"тип реквизита формы %q не распознан (%q): значение придёт в обработчик строкой",
						a.Name, a.TypeRef),
					SuggestedFix: "Названия типов латинские: string, string(N), number, decimal(P,S), date, dateTime, bool, " +
						"CatalogRef.<Справочник>, DocumentRef.<Документ>, EnumRef.<Перечисление>, AnyRef, ValueTable. " +
						"Русские написания («Строка», «Дата», «Число») загрузчик не понимает.",
				})
			}
		}
	}
	return issues
}

// formAttrTypeKnown — распознаётся ли тип реквизита формы. Сравнение по
// префиксу и без учёта регистра: «Date», «string(40)», «CatalogRef.Склад».
func formAttrTypeKnown(typeRef string) bool {
	t := strings.ToLower(strings.TrimSpace(typeRef))
	for _, known := range formAttrKnownTypes {
		if strings.HasPrefix(t, known) {
			return true
		}
	}
	return false
}
