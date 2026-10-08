package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

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
		if ent != nil {
			issues = append(issues, checkFormAttrTypes(ent.Name, ent.Forms)...)
		}
	}
	for _, proc := range proj.Processors {
		if proc != nil {
			issues = append(issues, checkFormAttrTypes(proc.Name, proc.Forms)...)
		}
	}
	return issues
}

func checkFormAttrTypes(owner string, forms []*metadata.FormModule) []Issue {
	var issues []Issue
	for _, form := range forms {
		if form == nil {
			continue
		}
		label := formAttrFileLabel(owner, form)
		for _, attr := range form.Attributes {
			if attr == nil || strings.TrimSpace(attr.TypeRef) == "" {
				continue
			}
			if !formAttrTypeKnown(attr.TypeRef) {
				issues = append(issues, formAttrTypeIssue(label, owner, fmt.Sprintf("реквизита формы %q", attr.Name), attr.TypeRef))
			}
			if attr.TypeRef != "ValueTable" {
				continue
			}
			for _, column := range attr.Columns {
				if column == nil || strings.TrimSpace(column.TypeRef) == "" || formValueTypeKnown(column.TypeRef) {
					continue
				}
				where := fmt.Sprintf("колонки %q реквизита формы %q", column.Name, attr.Name)
				issues = append(issues, formAttrTypeIssue(label, owner, where, column.TypeRef))
			}
		}
	}
	return issues
}

func formAttrTypeIssue(file, object, where, typeRef string) Issue {
	return Issue{
		File:   file,
		Object: object,
		Kind:   "Управляемая форма",
		Code:   "form.attr-type",
		Message: fmt.Sprintf(
			"тип %s не распознан (%q): значение придёт в обработчик строкой",
			where, typeRef),
		SuggestedFix: "Названия типов латинские: string, string(N), number, decimal(P), decimal(P,S), date, dateTime, bool, " +
			"CatalogRef.<Справочник>, DocumentRef.<Документ>, EnumRef.<Перечисление>, " +
			"ChartOfAccountsRef.<ПланСчетов>, AnyRef, ValueTable. " +
			"Русские написания («Строка», «Дата», «Число») загрузчик не понимает.",
	}
}

func formAttrFileLabel(owner string, form *metadata.FormModule) string {
	name := form.Name
	if name == "" {
		name = "объекта"
	}
	return "forms/" + strings.ToLower(owner) + "/" + name + ".form.yaml"
}

// formAttrTypeKnown повторяет фактическую грамматику реквизита формы.
// ValueTable проверяется отдельно и только в точном написании: часть runtime
// использует EqualFold, но formAttrIsScalar различает его регистрозависимо.
func formAttrTypeKnown(typeRef string) bool {
	t := strings.TrimSpace(typeRef)
	return t == "ValueTable" || formValueTypeKnown(t)
}

// formValueTypeKnown повторяет фактическое распознавание типов формы.
// Примитивы регистронезависимы и распознаются по префиксу, как
// typeFormAttrValue и typedempty.FromFormType: в частности, decimal(15) из
// канонического импорта 1С остаётся числом, а dateTime — датой. Ссылочные
// префиксы регистрозависимы, как attrRefEntityName.
func formValueTypeKnown(typeRef string) bool {
	t := strings.TrimSpace(typeRef)
	lower := strings.ToLower(t)
	for _, prefix := range []string{"string", "number", "decimal", "date", "bool"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	if t == "AnyRef" {
		return true
	}
	for _, prefix := range []string{"CatalogRef.", "DocumentRef.", "EnumRef.", "ChartOfAccountsRef."} {
		if strings.HasPrefix(t, prefix) {
			name := t[len(prefix):]
			return name != "" && name == strings.TrimSpace(name)
		}
	}
	return false
}
