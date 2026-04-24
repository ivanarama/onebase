package parser1c

import "strings"

// MapType конвертирует тип 1С → тип onebase.
// Возвращает тип и примечание (для отчёта).
func MapType(t FieldType1C) (onebaseType string, note string) {
	if t.Composite {
		return "string", "составной тип → string"
	}

	p := t.Primary
	switch {
	case p == "String" || p == "Строка":
		return "string", ""
	case p == "Number" || p == "Число":
		return "number", ""
	case p == "Date" || p == "Дата":
		return "date", ""
	case p == "Boolean" || p == "Булево":
		return "bool", ""
	case p == "ValueStorage" || p == "ХранилищеЗначения":
		return "string", "ХранилищеЗначения → string"
	case strings.HasPrefix(p, "CatalogRef.") || strings.HasPrefix(p, "СправочникСсылка."):
		obj := extractRefName(p)
		if t.RefObject != "" {
			obj = t.RefObject
		}
		return "reference:" + obj, ""
	case strings.HasPrefix(p, "DocumentRef.") || strings.HasPrefix(p, "ДокументСсылка."):
		obj := extractRefName(p)
		if t.RefObject != "" {
			obj = t.RefObject
		}
		return "reference:" + obj, ""
	case strings.HasPrefix(p, "EnumRef.") || strings.HasPrefix(p, "ПеречислениеСсылка."):
		return "string", "перечисление → string"
	case p == "":
		return "string", ""
	default:
		return "string", "неизвестный тип " + p + " → string"
	}
}

func extractRefName(s string) string {
	parts := strings.SplitN(s, ".", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return s
}
