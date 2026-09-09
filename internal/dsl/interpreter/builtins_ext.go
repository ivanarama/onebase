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
	// Строку формата НЕ приводим к нижнему регистру: шаблон даты
	// регистрозависим (MM — месяц, mm — минуты, HH — часы), а ключи «ДФ=»/
	// «ЧДЦ=» ищет сам extractFormatParam, без учёта регистра.
	fmtStr := strArg(args, 1)
	fmtKeys := strings.ToLower(fmtStr)

	// Date formatting
	if strings.Contains(fmtKeys, "дф=") || strings.Contains(fmtKeys, "df=") {
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
	// Ключ («дф=», «чдц=») ищем без учёта регистра, а значение отдаём из
	// ИСХОДНОЙ строки: шаблон даты регистрозависим. Индексы нижнего регистра
	// годятся для исходной строки, пока приведение не изменило длину (для
	// латиницы и кириллицы это так); иначе честно работаем по нижнему регистру
	// — прежнее поведение, регистр значения при этом теряется.
	lowered := strings.ToLower(fmtStr)
	if len(lowered) != len(fmtStr) {
		fmtStr = lowered
	}
	idx := strings.Index(lowered, key)
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
// formatDate переводит шаблон 1С в раскладку Go. Токены РЕГИСТРОЗАВИСИМЫ, как в
// 1С: «MM» — месяц, «mm» — минуты, «HH» — часы 24-часовые, «hh» — 12-часовые.
// Русские написания («дд.ММ.гггг ЧЧ:мм:сс») понимаются наравне с латинскими.
//
// РАНЬШЕ ВРЕМЕНИ НЕ БЫЛО ВОВСЕ: строка формата приводилась к нижнему регистру
// целиком, и в замене участвовали только yyyy/yy/mm/dd. Привычное
// «ДФ=dd.MM.yyyy HH:mm» печатало «09.09.2026 hh:09» — «hh» уходило в вывод
// литералом, а на месте минут оказывался МЕСЯЦ. Ошибки при этом не было, поэтому
// такая метка времени спокойно доезжала до пользователя.
//
// СОВМЕСТИМОСТЬ. До этой правки строчное «mm» означало МЕСЯЦ (регистр не
// различался), и конфигурации с «ДФ=dd.mm.yyyy» существуют. Прочитать его как
// минуты значило бы молча заменить месяц минутами — ровно тот дефект, который
// здесь и чинится. Поэтому строчное «mm»/«мм» читается минутами только там, где
// в шаблоне ЕСТЬ часы: без часов минуты бессмысленны, а «дд.мм.гггг» продолжает
// печатать месяц. Явное «MM» — всегда месяц, «HH:mm» — всегда часы и минуты.
func formatDate(t time.Time, pattern string) string {
	// Замены не перекрываются и не перечитывают уже подставленное, а порядок
	// аргументов задаёт приоритет: «yyyy» пробуется раньше «yy», иначе год
	// превратился бы в «0606».
	minute := goLayoutMonth
	if hasHourToken(pattern) {
		minute = goLayoutMinute
	}
	return t.Format(strings.NewReplacer(
		"yyyy", goLayoutYear4, "гггг", goLayoutYear4,
		"yy", goLayoutYear2, "гг", goLayoutYear2,
		"MM", goLayoutMonth, "ММ", goLayoutMonth,
		"dd", goLayoutDay, "дд", goLayoutDay,
		"HH", goLayoutHour24, "ЧЧ", goLayoutHour24,
		"hh", goLayoutHour12, "чч", goLayoutHour12,
		"ss", goLayoutSecond, "сс", goLayoutSecond,
		"mm", minute, "мм", minute,
	).Replace(pattern))
}

// Токены эталонного времени Go (Mon Jan 2 15:04:05 MST 2006) — именами, чтобы
// таблица замен читалась как «год → год», а не как набор чисел.
const (
	goLayoutYear4  = "2006"
	goLayoutYear2  = "06"
	goLayoutMonth  = "01"
	goLayoutDay    = "02"
	goLayoutHour24 = "15"
	goLayoutHour12 = "03"
	goLayoutMinute = "04"
	goLayoutSecond = "05"
)

// hasHourToken — есть ли в шаблоне часы. От этого зависит прочтение строчного
// «mm»: минуты или (по совместимости) месяц.
func hasHourToken(pattern string) bool {
	for _, tok := range []string{"HH", "hh", "ЧЧ", "чч"} {
		if strings.Contains(pattern, tok) {
			return true
		}
	}
	return false
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
