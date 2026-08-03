package onec_forms

import (
	"fmt"
	"strconv"
	"strings"
)

// Type1CToOneBase нормализует тип из Form.xml в нейтральную нотацию OneBase.
// Примеры:
//
//	"xs:string"             + qualifiers Length=40 Variable    → "string(40)"
//	"xs:decimal"            + qualifiers Length=15 Precision=2 → "decimal(15,2)"
//	"xs:dateTime"           +                                 → "dateTime"
//	"xs:boolean"            +                                 → "bool"
//	"cfg:CatalogRef.X"      +                                 → "CatalogRef.X"
//	"cfg:DocumentRef.X"     +                                 → "DocumentRef.X"
//	"v8:ValueTable"         +                                 → "ValueTable"
//	"cfg:AnyRef"            +                                 → "AnyRef" + W021
//	composite (несколько <Type>)                              → "string" + W020
//
// Если соответствие неизвестно — возвращает исходную строку и W022.
func Type1CToOneBase(xsdType string, length, precision int, allowedLength string) string {
	t := strings.TrimSpace(xsdType)

	// Префикс xs: — простые скалярные типы.
	if strings.HasPrefix(t, "xs:") {
		base := strings.TrimPrefix(t, "xs:")
		switch base {
		case "string":
			if length > 0 {
				return fmt.Sprintf("string(%d)", length)
			}
			return "string"
		case "decimal":
			if length > 0 && precision > 0 {
				return fmt.Sprintf("decimal(%d,%d)", length, precision)
			}
			if length > 0 {
				return fmt.Sprintf("decimal(%d)", length)
			}
			return "number"
		case "dateTime", "date":
			return base
		case "boolean":
			return "bool"
		}
		return base
	}

	// Префикс cfg: — ссылки на объекты метаданных.
	if strings.HasPrefix(t, "cfg:") {
		return strings.TrimPrefix(t, "cfg:")
	}

	// Префикс v8: — встроенные типы платформы.
	if strings.HasPrefix(t, "v8:") {
		return strings.TrimPrefix(t, "v8:")
	}

	return t
}

// TypeOneBaseTo1C — обратное направление. Возвращает XSD-тип + qualifiers
// в раздельных значениях (typeName, length, precision, allowedLength).
func TypeOneBaseTo1C(neutral string) (typeName string, length int, precision int, allowedLength string) {
	t := strings.TrimSpace(neutral)
	// "string(40)" / "decimal(15,2)" / "decimal(15)"
	if idx := strings.Index(t, "("); idx > 0 && strings.HasSuffix(t, ")") {
		base := t[:idx]
		params := t[idx+1 : len(t)-1]
		switch base {
		case "string":
			// Неразбираемая длина оставляет 0 — прежний и намеренный контракт:
			// спецификация типа приходит из уже проверенной модели, а 0 здесь
			// означает «без ограничения».
			length = atoiOrZero(params)
			return "xs:string", length, 0, "Variable"
		case "decimal":
			parts := strings.Split(params, ",")
			if len(parts) >= 1 {
				length = atoiOrZero(parts[0])
			}
			if len(parts) >= 2 {
				precision = atoiOrZero(parts[1])
			}
			return "xs:decimal", length, precision, ""
		}
	}
	switch t {
	case "string":
		return "xs:string", 0, 0, "Variable"
	case "number":
		return "xs:decimal", 0, 0, ""
	case "dateTime", "date":
		return "xs:" + t, 0, 0, ""
	case "bool", "boolean":
		return "xs:boolean", 0, 0, ""
	case "ValueTable", "ValueList":
		return "v8:" + t, 0, 0, ""
	}
	// CatalogRef.X / DocumentRef.X / EnumRef.X / ChartOfAccountsRef.X / AnyRef
	if strings.Contains(t, "Ref") {
		return "cfg:" + t, 0, 0, ""
	}
	return t, 0, 0, ""
}

// atoiOrZero возвращает число или 0, если строка им не является. Ноль здесь —
// осмысленное значение «без ограничения», а не признак ошибки, поэтому отдельно
// сообщать не о чем.
func atoiOrZero(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}
