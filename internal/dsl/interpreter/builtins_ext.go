package interpreter

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// isBlankVal checks if a value is considered empty (nil, "", 0, false, empty collection).
func isBlankVal(v any) bool {
	if v == nil {
		return true
	}
	// Числовой ноль пуст в любом Go-типе (см. truthy): булево поле из запроса на
	// SQLite приходит как int64, и ЗначениеЗаполнено(Ложь) отвечало «истина».
	if zero, ok := numericZero(v); ok {
		return zero
	}
	switch t := v.(type) {
	case string:
		return t == ""
	case bool:
		return !t
	case []any:
		return len(t) == 0
	case *Array:
		return len(t.items) == 0
	case *Map:
		return len(t.keys) == 0
	case *Ref:
		return isEmptyRefUUID(t.UUID)
	}
	return false
}

// isEmptyRefVal — узкое определение «пустой ссылки», в отличие от isBlankVal,
// которая считает пустым и 0, и false. Используется в ПустаяСсылка(x).
func isEmptyRefVal(v any) bool {
	if v == nil {
		return true
	}
	switch t := v.(type) {
	case string:
		return isEmptyRefUUID(t)
	case *Ref:
		return isEmptyRefUUID(t.UUID)
	}
	return false
}

// isEmptyRefUUID — пустой UUID = "" или нули.
func isEmptyRefUUID(s string) bool {
	return s == "" || s == "00000000-0000-0000-0000-000000000000"
}

// formatValue implements Формат(value, formatString) with minimal format support.
func fmtBuiltin(args []any) (string, error) {
	return fmtBuiltinBounded(args, 0)
}

func fmtBuiltinBounded(args []any, maxDecimalPlaces int32) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	if len(args) < 2 {
		return fmt.Sprintf("%v", args[0]), nil
	}
	val := args[0]
	if maxDecimalPlaces > 0 && isNumeric(val) {
		d, _ := toDecimal(val)
		if !decimalWithinExpansionBounds(d, maxDecimalPlaces) {
			return "", fmt.Errorf("формат: число вне безопасного диапазона")
		}
	}
	fmtStr := strings.ToLower(strArg(args, 1))

	// Date formatting
	if strings.Contains(fmtStr, "дф=") || strings.Contains(fmtStr, "df=") {
		if t, ok := toTime(args, 0); ok {
			pattern := extractFormatParam(fmtStr, "дф=")
			if pattern == "" {
				pattern = extractFormatParam(fmtStr, "df=")
			}
			return formatDate(t, pattern), nil
		}
	}

	// Number formatting
	if f, ok := toFloat(val); ok {
		decimals := 2
		if d := extractFormatParam(fmtStr, "чдц="); d != "" {
			if n, err := strconv.Atoi(d); err == nil {
				if maxDecimalPlaces > 0 && (n < -int(maxDecimalPlaces) || n > int(maxDecimalPlaces)) {
					return "", fmt.Errorf("формат: точность вне безопасного диапазона")
				}
				decimals = n
			}
		}
		sep := " "
		if s := extractFormatParam(fmtStr, "чрг="); s != "" {
			if maxDecimalPlaces > 0 && len(s) > 64 {
				return "", fmt.Errorf("формат: разделитель разрядов слишком длинный")
			}
			sep = s
		}
		return formatNumber(f, decimals, sep), nil
	}

	return fmt.Sprintf("%v", val), nil
}

// extractFormatParam extracts a parameter value from a format string like "ЧДЦ=2; ЧРГ=' '"
func extractFormatParam(fmtStr, key string) string {
	idx := strings.Index(fmtStr, key)
	if idx < 0 {
		return ""
	}
	rest := fmtStr[idx+len(key):]
	// Skip optional quote
	if len(rest) > 0 && rest[0] == '\'' {
		rest = rest[1:]
		end := strings.Index(rest, "'")
		if end >= 0 {
			return rest[:end]
		}
	}
	// Read until ; or end
	end := strings.Index(rest, ";")
	if end >= 0 {
		return rest[:end]
	}
	return rest
}

// formatDate converts a 1C-style date pattern to Go format and formats.
//
// Шаблон приходит сюда уже в нижнем регистре: fmtBuiltinBounded понижает регистр
// ВСЕЙ строки формата, чтобы ключи «ДФ=»/«ЧДЦ=» распознавались в любом написании.
// Поэтому месяц ищется как «mm» — верхнерегистровая «MM» до этой функции не доходит,
// и отдельная замена для неё была бы мёртвым кодом. Порядок важен: «yyyy» заменяется
// раньше «yy», иначе год превратился бы в «0606».
func formatDate(t time.Time, pattern string) string {
	// Convert 1C patterns to Go
	goFmt := pattern
	goFmt = strings.ReplaceAll(goFmt, "yyyy", "2006")
	goFmt = strings.ReplaceAll(goFmt, "yy", "06")
	goFmt = strings.ReplaceAll(goFmt, "mm", "01")
	goFmt = strings.ReplaceAll(goFmt, "dd", "02")
	return t.Format(goFmt)
}

// formatNumber formats a float with given decimal places and thousands separator.
func formatNumber(f float64, decimals int, sep string) string {
	s := strconv.FormatFloat(f, 'f', decimals, 64)
	parts := strings.Split(s, ".")
	intPart := parts[0]
	if sep != "" && len(intPart) > 3 {
		sign := ""
		if intPart[0] == '-' {
			sign = "-"
			intPart = intPart[1:]
		}
		var buf []byte
		for i, c := range intPart {
			if i > 0 && (len(intPart)-i)%3 == 0 {
				buf = append(buf, sep...)
			}
			buf = append(buf, byte(c)) //nolint:gosec // G115: значение приходит из проверенной модели и заведомо укладывается в целевой тип
		}
		intPart = sign + string(buf)
	}
	if len(parts) > 1 {
		return intPart + "." + parts[1]
	}
	return intPart
}
