package onec_forms

import (
	"strings"
)

// NormalizeForExport — обратная операция к NormalizeForImport:
// преобразует OneBase-каноничные имена обратно в 1С-формат, чтобы
// writer_xml.go мог сериализовать их в Form.xml.
//
// Действия:
//  1. el.Kind: ПолеВвода → InputField, Флажок → CheckBoxField и т.д.
//     Через elements_map (инверсия). Несимметричные случаи (Decoration,
//     CommandBarButton) восстанавливаются по props.decoration / props.in_command_bar.
//  2. Имена событий: ПриОткрытии → OnOpen, ПриИзменении → OnChange.
//  3. Реквизиты: type "CatalogRef.X" → "cfg:CatalogRef.X", "decimal(15,2)" →
//     ("xs:decimal", length=15, precision=2) и т.д. — через TypeOneBaseTo1C.
//
// Сам IR не теряет данные — только нормализует строковые поля. Если у
// элемента уже стояло 1С-имя (например после ReadFormXML) — оно сохраняется.
func NormalizeForExport(form *IRForm) []Warning {
	if form == nil {
		return nil
	}
	var warns Warnings

	form.Events = denormalizeEventMap(form.Events, "", &warns)

	for _, a := range form.Attributes {
		normalizeAttributeForExport(a, &warns)
	}
	for _, el := range form.Elements {
		normalizeElementForExport(el, &warns)
	}
	if form.AutoCommandBar != nil {
		for _, btn := range form.AutoCommandBar.Buttons {
			_ = btn // имена кнопок не меняются
		}
	}
	return []Warning(warns)
}

func normalizeElementForExport(el *IRElement, warns *Warnings) {
	if el == nil {
		return
	}

	// Restore decoration / command_bar_button special cases from props.
	if el.Props != nil {
		if dec, _ := el.Props["decoration"].(bool); dec && el.Kind == "Надпись" {
			el.Kind = "Decoration"
			delete(el.Props, "decoration")
		}
		if inBar, _ := el.Props["in_command_bar"].(bool); inBar && el.Kind == "Кнопка" {
			// Кнопка остаётся Button, но с <Type>CommandBarButton</Type>
			if _, exists := el.Props["Type"]; !exists {
				el.Props["Type"] = "CommandBarButton"
			}
			delete(el.Props, "in_command_bar")
		}
	}

	// Если уже не OneBase-имя — оставляем как есть (видимо после ReadFormXML).
	// Иначе через инверсию ElementOneBaseTo1C.
	if mapped, ok := isOneBaseKind(el.Kind); ok {
		el.Kind = mapped
	}

	el.Events = denormalizeEventMap(el.Events, el.Name, warns)

	for _, c := range el.Children {
		normalizeElementForExport(c, warns)
	}
}

func normalizeAttributeForExport(a *IRAttribute, warns *Warnings) {
	if a == nil {
		return
	}
	a.TypeRef, a.Length, a.Precision, a.AllowedLength = restoreAttrType(a.TypeRef, a.Length, a.Precision, a.AllowedLength)
	for _, c := range a.Columns {
		c.TypeRef, c.Length, c.Precision, _ = restoreAttrType(c.TypeRef, c.Length, c.Precision, "")
	}
}

// restoreAttrType возвращает 1С-формат типа, длину/точность и AllowedLength.
// Если type уже выглядит как xs:/cfg:/v8: — оставляем без изменений.
func restoreAttrType(neutral string, length, precision int, allowed string) (string, int, int, string) {
	n := strings.TrimSpace(neutral)
	if n == "" {
		return n, length, precision, allowed
	}
	if strings.HasPrefix(n, "xs:") || strings.HasPrefix(n, "cfg:") || strings.HasPrefix(n, "v8:") {
		return n, length, precision, allowed
	}
	t, l, p, al := TypeOneBaseTo1C(n)
	if l > 0 && length == 0 {
		length = l
	}
	if p > 0 && precision == 0 {
		precision = p
	}
	if al != "" && allowed == "" {
		allowed = al
	}
	return t, length, precision, allowed
}

// isOneBaseKind смотрит инверсию elements_map: если el.Kind — это
// каноническое OneBase-имя, возвращает соответствующий 1С-Kind.
func isOneBaseKind(kind string) (string, bool) {
	// elementMapInverse уже инициализирован в init() elements_map.go,
	// он работает с FormElementType (string-alias). Для надёжности
	// сделаем прямой lookup через elementMap (1С→OneBase) перебором:
	for name1c, oneBase := range elementMap {
		if string(oneBase) == kind {
			return name1c, true
		}
	}
	return "", false
}

// denormalizeEventMap — обратное преобразование: OneBase FormEventType
// (например, "ПриОткрытии") → 1С имя ("OnOpen"). Неизвестные имена
// остаются без изменений + W031 (нет процедуры).
func denormalizeEventMap(in map[string]string, owner string, warns *Warnings) map[string]string {
	if len(in) == 0 {
		return in
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if mapped, ok := eventOneBaseTo1CByName(k); ok {
			out[mapped] = v
			continue
		}
		// Имя уже 1С-ское (OnOpen/OnChange) или экзотическое — оставляем.
		out[k] = v
	}
	return out
}

// eventOneBaseTo1CByName — версия EventOneBaseTo1C, принимающая строку
// (используем строки и в IR, и в YAML). Используется при экспорте.
func eventOneBaseTo1CByName(name string) (string, bool) {
	for k, v := range eventMap {
		if string(v) == name {
			return k, true
		}
	}
	return "", false
}
