package interpreter

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// dateBuiltin wraps a function that transforms a time.Time value.
func dateBuiltin(fn func(time.Time) any) func([]any, string, int) (any, error) {
	return func(args []any, _ string, _ int) (any, error) {
		if t, ok := toTime(args, 0); ok {
			return fn(t), nil
		}
		return nil, nil
	}
}

func dateEndMonth(args []any, _ string, _ int) (any, error) {
	t, ok := toTime(args, 0)
	if !ok {
		return nil, nil
	}
	nm := t.Month() + 1
	ny := t.Year()
	if nm > 12 {
		nm = 1
		ny++
	}
	return time.Date(ny, nm, 1, 0, 0, 0, 0, time.Local).Add(-time.Second), nil
}

func dateBegWeek(args []any, _ string, _ int) (any, error) {
	t, ok := toTime(args, 0)
	if !ok {
		return nil, nil
	}
	o := (int(t.Weekday()) - int(time.Monday) + 7) % 7
	return time.Date(t.Year(), t.Month(), t.Day()-o, 0, 0, 0, 0, time.Local), nil
}

func dateEndWeek(args []any, _ string, _ int) (any, error) {
	t, ok := toTime(args, 0)
	if !ok {
		return nil, nil
	}
	o := (int(time.Sunday) - int(t.Weekday()) + 7) % 7
	return time.Date(t.Year(), t.Month(), t.Day()+o, 23, 59, 59, 0, time.Local), nil
}

func addMonthBuiltin(args []any, _ string, _ int) (any, error) {
	t, ok := toTime(args, 0)
	if !ok {
		return nil, nil
	}
	return addCalendarDate(t, 0, calendarShiftArg(args), 0), nil
}

// addDayBuiltin — ДобавитьДень(дата, n). n может быть отрицательным.
func addDayBuiltin(args []any, _ string, _ int) (any, error) {
	t, ok := toTime(args, 0)
	if !ok {
		return nil, nil
	}
	return addCalendarDate(t, 0, 0, calendarShiftArg(args)), nil
}

const maxCalendarShift = math.MaxInt32 / 2

// calendarShiftArg сохраняет прежнее усечение дробной части к нулю, но не
// допускает implementation-dependent float64 -> int overflow. Граница в
// половину int32 едина на всех поддерживаемых архитектурах, оставляет запас
// для календарных компонентов time.AddDate и всё равно намного шире любого
// прикладного сдвига.
func calendarShiftArg(args []any) int {
	if len(args) < 2 {
		return 0
	}
	shift, ok := toFloat(args[1])
	if !ok {
		if isNumeric(args[1]) {
			RaiseUserError("календарный сдвиг: число вне безопасного диапазона")
		}
		// Совместимость с floatArg: нечисловое значение означало нулевой сдвиг.
		return 0
	}
	shift = math.Trunc(shift)
	if shift > float64(maxCalendarShift) || shift < -float64(maxCalendarShift) {
		RaiseUserError("календарный сдвиг выходит за безопасный диапазон")
	}
	return int(shift)
}

// safeDateResult не выпускает time.Time, который затем невозможно отдать как
// RFC 3339/JSON. time.Date, Add и AddDate умеют считать за четырёхзначными
// годами, но такой результат падает уже при сериализации вне Попытка/Исключение.
func safeDateResult(t time.Time) time.Time {
	if t.Year() < 0 || t.Year() > 9999 {
		RaiseUserError("результат даты выходит за безопасный диапазон 0000..9999")
	}
	return t
}

func addCalendarDate(t time.Time, years, months, days int) time.Time {
	return safeDateResult(t.AddDate(years, months, days))
}

// Сдвиг по часам, минутам и секундам (issue #707). Раньше их не было, а
// очевидные обходные пути не работали: ДобавитьДень(Дата, 60/86400) молча
// отбрасывал дробную часть (int()), а Дата(Г,М,Д,ч,м,с+90) не нормализует
// переполнение и возвращала пустую дату. Работал только `Дата + N` — но нигде
// не был описан, и находился перебором.
//
// Доли принимаются: ДобавитьСекунд(Д, 0.5) сдвигает на полсекунды. Это не
// прихоть, а следствие того, что в DSL число одно — дробное; отбрасывать
// дробь молча значило бы повторить ровно ту беду, из-за которой issue и
// заведён.
func addSecondsBuiltin(args []any, _ string, _ int) (any, error) {
	return shiftBySeconds(args, 1)
}

func addMinutesBuiltin(args []any, _ string, _ int) (any, error) {
	return shiftBySeconds(args, 60)
}

func addHoursBuiltin(args []any, _ string, _ int) (any, error) {
	return shiftBySeconds(args, 3600)
}

func shiftBySeconds(args []any, unit float64) (any, error) {
	t, ok := toTime(args, 0)
	if !ok {
		return nil, nil
	}
	seconds := float64(0)
	if len(args) > 1 {
		var numericOK bool
		seconds, numericOK = toFloat(args[1])
		if !numericOK {
			if isNumeric(args[1]) {
				RaiseUserError("сдвиг даты: число вне безопасного диапазона")
			}
			// Совместимость: нечисловой аргумент раньше молча означал нулевой
			// сдвиг через floatArg.
			seconds = 0
		}
	}
	scaled := seconds * unit
	return dateAddSeconds(t, scaled), nil
}

// addYearBuiltin — ДобавитьГод(дата, n). n может быть отрицательным.
func addYearBuiltin(args []any, _ string, _ int) (any, error) {
	t, ok := toTime(args, 0)
	if !ok {
		return nil, nil
	}
	return addCalendarDate(t, calendarShiftArg(args), 0, 0), nil
}

// dateLayouts — строковые форматы, понимаемые конструктором Дата().
var dateLayouts = []string{
	"2006-01-02T15:04:05", "2006-01-02 15:04:05-07:00", "2006-01-02 15:04:05", "2006-01-02T15:04",
	"2006-01-02", "02.01.2006 15:04:05", "02.01.2006",
	"20060102150405", "20060102", time.RFC3339,
}

// parseDateString разбирает строку в дату по набору распространённых форматов.
func parseDateString(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, l := range dateLayouts {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// numericDateString converts the two compact numeric forms understood by
// Дата: YYYYMMDD and YYYYMMDDhhmmss. A decimal value drops leading zeroes, so
// the full form produced by Число(Дата) needs them restored for years 0001–0999.
// Bounds are checked before Decimal.StringFixed: shopspring/decimal accepts
// extreme exponents, and expanding one here would otherwise exhaust memory.
func numericDateString(v any) (string, bool) {
	if f, ok := v.(float64); ok && (math.IsNaN(f) || math.IsInf(f, 0)) {
		return "", false
	}
	d, ok := toDecimal(v)
	if !ok || d.Sign() <= 0 {
		return "", false
	}
	coefficient := d.Coefficient()
	if coefficient.BitLen() > 128 {
		return "", false
	}
	// Деление Decimal сохраняет DivisionPrecision во внутреннем exponent:
	// 20260511/1 численно целое, но представлено коэффициентом с хвостовыми
	// нулями и отрицательным exponent. Нормализуем только уже имеющиеся нули,
	// не вызывая IsInteger/StringFixed: на pathological exponent они способны
	// развернуть гигантскую степень десяти.
	digits := coefficient.String()
	trimmed := strings.TrimRight(digits, "0")
	effectiveExp := int64(d.Exponent()) + int64(len(digits)-len(trimmed))
	if effectiveExp < 0 {
		return "", false
	}
	digitCount := int64(len(trimmed)) + effectiveExp
	if digitCount != 8 && (digitCount < 11 || digitCount > 14) {
		return "", false
	}
	s := trimmed + strings.Repeat("0", int(effectiveExp))
	switch len(s) {
	case 8, 14:
		// Already a complete date or date-time representation.
	case 11, 12, 13:
		// Число(Дата) loses only the leading zeroes of a valid four-digit year.
		s = strings.Repeat("0", 14-len(s)) + s
	default:
		return "", false
	}
	return s, true
}

// dateComponentArg preserves the constructor's historical truncation and
// non-numeric-to-zero compatibility, but avoids implementation-dependent
// float64-to-int overflow before time.Date performs documented normalization.
func dateComponentArg(args []any, i int) int {
	if i >= len(args) {
		return 0
	}
	component, ok := toFloat(args[i])
	if !ok {
		if isNumeric(args[i]) {
			RaiseUserError("компонент даты: число вне безопасного диапазона")
		}
		return 0
	}
	component = math.Trunc(component)
	if component > float64(maxCalendarShift) || component < -float64(maxCalendarShift) {
		RaiseUserError("компонент даты выходит за безопасный диапазон")
	}
	return int(component)
}

// dateConstructor реализует функцию Дата():
//
//	Дата(Год, Месяц, День[, Час, Минута, Секунда])
//	Дата("2026-05-11") / Дата("20260511") / Дата(20260511)
//	Дата(дата) — идемпотентно
//
// Неподдерживаемый или неразбираемый одноаргументный ввод даёт пустую дату
// (time.Time{}), как Дата(0) в 1С. Небезопасные числовые компоненты и результат
// с годом вне 0000..9999 дают ловимую DSL-ошибку.
func dateConstructor(args []any, _ string, _ int) (any, error) {
	if len(args) == 1 {
		switch v := args[0].(type) {
		case time.Time:
			return safeDateResult(v), nil
		case string:
			if t, ok := parseDateString(v); ok {
				return t, nil
			}
			return time.Time{}, nil
		default:
			if s, ok := numericDateString(v); ok {
				if t, ok := parseDateString(s); ok {
					return t, nil
				}
			}
			return time.Time{}, nil
		}
	}
	if len(args) < 3 {
		return time.Time{}, nil
	}
	y := dateComponentArg(args, 0)
	mo := dateComponentArg(args, 1)
	d := dateComponentArg(args, 2)
	if mo < 1 {
		mo = 1
	}
	if d < 1 {
		d = 1
	}
	return safeDateResult(time.Date(y, time.Month(mo), d,
		dateComponentArg(args, 3), dateComponentArg(args, 4), dateComponentArg(args, 5),
		0, time.Local)), nil
}

func dateDiffBuiltin(args []any, _ string, _ int) (any, error) {
	t1, ok1 := toTime(args, 0)
	t2, ok2 := toTime(args, 1)
	if !ok1 || !ok2 {
		return float64(0), nil
	}
	unit := strings.ToLower(strArg(args, 2))
	d := t2.Sub(t1)
	switch unit {
	case "секунда", "second":
		return float64(int(d.Seconds())), nil
	case "минута", "minute":
		return float64(int(d.Minutes())), nil
	case "час", "hour":
		return float64(int(d.Hours())), nil
	case "месяц", "month":
		m := (t2.Year()-t1.Year())*12 + int(t2.Month()) - int(t1.Month())
		return float64(m), nil
	case "год", "year":
		return float64(t2.Year() - t1.Year()), nil
	default:
		return float64(int(d.Hours()) / 24), nil
	}
}

func joinBuiltin(args []any, _ string, _ int) (any, error) {
	sep := strArg(args, 1)
	var parts []string
	if arr, ok := args[0].(*Array); ok {
		for _, v := range arr.Iterate() {
			parts = append(parts, fmt.Sprintf("%v", v))
		}
	} else if arr, ok := args[0].([]any); ok {
		for _, v := range arr {
			parts = append(parts, fmt.Sprintf("%v", v))
		}
	}
	return strings.Join(parts, sep), nil
}
