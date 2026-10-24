package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckFieldTypes предупреждает о типе реквизита, которого загрузчик не знает.
//
// ПОЧЕМУ ЭТО НУЖНО. Нераспознанный тип не ошибка загрузки: поле молча создаётся
// текстовой колонкой. На SQLite разница почти не видна — даты там и так текст, —
// а на PostgreSQL реквизит `type: datetime` становится `text`, и запрос
// «Поле >= &Дата» падает: operator does not exist: text >= timestamp with time
// zone. То же с опечаткой (`nubmer`) и с `text`: полнотекстовый поиск, подписи
// и умолчания ждут именно `string`, поэтому `text` ведёт себя не как строка.
//
// ПРЕДУПРЕЖДЕНИЕ, А НЕ ОШИБКА — по той же причине, что form.attr-type: такой
// реквизит сегодня работает как текст, и сломать сборку рабочих конфигураций
// значило бы наказать за то, что платформа сама разрешала. Предупреждение
// печатает обычный `onebase check`, без --lint: иначе его увидит только тот,
// кто уже знает, что искать.
func CheckFieldTypes(proj *project.Project) []Issue {
	if proj == nil {
		return nil
	}
	var issues []Issue
	report := func(file, object, kind, where string, fields []metadata.Field) {
		for _, f := range fields {
			if fieldTypeKnown(f) {
				continue
			}
			issues = append(issues, Issue{
				File:   file,
				Object: object,
				Kind:   kind,
				Code:   "field.type",
				Message: fmt.Sprintf("тип %s %q не распознан (%q): колонка создаётся текстовой, "+
					"сравнение с датой и числом на PostgreSQL не работает", where, f.Name, string(f.Type)),
				SuggestedFix: "Типы реквизитов: string, number, number(L,P), decimal(L,P), date (дата со временем), bool, " +
					"richtext, image, reference:<Объект>, enum:<Перечисление>.",
			})
		}
	}
	for _, e := range proj.Entities {
		if e == nil {
			continue
		}
		kind := "Справочник"
		if e.Kind == metadata.KindDocument {
			kind = "Документ"
		}
		file := entityFileLabel(e)
		report(file, e.Name, kind, "реквизита", e.Fields)
		for _, tp := range e.TableParts {
			report(file, e.Name, kind, fmt.Sprintf("реквизита табличной части %q", tp.Name), tp.Fields)
		}
	}
	for _, r := range proj.Registers {
		if r == nil {
			continue
		}
		file := "registers/" + r.Name + ".yaml"
		report(file, r.Name, "Регистр", "измерения", r.Dimensions)
		report(file, r.Name, "Регистр", "ресурса", r.Resources)
		report(file, r.Name, "Регистр", "реквизита", r.Attributes)
	}
	for _, ir := range proj.InfoRegisters {
		if ir == nil {
			continue
		}
		file := "inforegs/" + ir.Name + ".yaml"
		report(file, ir.Name, "Регистр сведений", "измерения", ir.Dimensions)
		report(file, ir.Name, "Регистр сведений", "ресурса", ir.Resources)
	}
	for _, c := range proj.Constants {
		if c == nil {
			continue
		}
		report("constants", c.Name, "Константа", "константы",
			[]metadata.Field{{Name: c.Name, Type: c.Type, RefEntity: c.RefEntity, EnumName: c.EnumName}})
	}
	return issues
}

// fieldTypeKnown повторяет фактическую грамматику parseField: ссылка и
// перечисление распознаются по префиксу, number(L,P)/decimal(L,P) уже сведены к
// number. Пустой тип не проверяется — его разбирает валидатор метаданных.
func fieldTypeKnown(f metadata.Field) bool {
	if f.RefEntity != "" || f.EnumName != "" || strings.TrimSpace(string(f.Type)) == "" {
		return true
	}
	switch f.Type {
	case metadata.FieldTypeString, metadata.FieldTypeNumber, metadata.FieldTypeDate,
		metadata.FieldTypeBool, metadata.FieldTypeRichText, metadata.FieldTypeImage:
		return true
	}
	return false
}
