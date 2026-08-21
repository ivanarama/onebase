package ui

import (
	"fmt"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/richtext"
)

// fieldDisplayText — ОДНА точка превращения значения реквизита в текст для
// показа. До #1076 таких точек было несколько, и они расходились: одни ветки
// разбирали тип, другие звали типобезразличный fmtReportCell.
//
// Разбор по типу здесь обязателен, а не желателен: без типа поля отличить
// int64(1)-число от int64(1)-булева невозможно. Драйверы отдают bool по-разному
// (SQLite — числом, pgx — булевым), поэтому типобезразличный путь показывал «1»
// на SQLite против «true» на PostgreSQL в обычном списке справочника —
// замерено, а не выведено из кода.
//
// enumLabels может быть nil: тогда значение перечисления показывается как есть.
func fieldDisplayText(f metadata.Field, v any, enumLabels map[string]map[string]string) string {
	if v == nil {
		return ""
	}
	if f.EnumName != "" || metadata.IsEnum(f.Type) {
		return enumLabelFor(enumLabels, f.Name, fmt.Sprintf("%v", v))
	}
	if metadata.IsRichText(f.Type) {
		return truncateDisplayRunes(richtext.Plaintext(fmt.Sprintf("%v", v)), 100)
	}
	switch f.Type {
	case metadata.FieldTypeBool:
		if asBool(v) {
			return "✓"
		}
		return "—"
	case metadata.FieldTypeDate:
		return fmtDateValue(v)
	}
	return fmtReportCell(v)
}

// truncateDisplayRunes режет длинный текст по РУНАМ, а не по байтам: срез по
// байтам разорвал бы кириллицу посреди символа.
func truncateDisplayRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}
