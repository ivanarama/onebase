package interpreter

import (
	"fmt"
	"strconv"
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
	return t.AddDate(0, int(floatArg(args, 1)), 0), nil
}

// addDayBuiltin — ДобавитьДень(дата, n). n может быть отрицательным.
func addDayBuiltin(args []any, _ string, _ int) (any, error) {
	t, ok := toTime(args, 0)
	if !ok {
		return nil, nil
	}
	return t.AddDate(0, 0, int(floatArg(args, 1))), nil
}

// addYearBuiltin — ДобавитьГод(дата, n). n может быть отрицательным.
func addYearBuiltin(args []any, _ string, _ int) (any, error) {
	t, ok := toTime(args, 0)
	if !ok {
		return nil, nil
	}
	return t.AddDate(int(floatArg(args, 1)), 0, 0), nil
}

// addTimeBuiltin строит ДобавитьЧас/ДобавитьМинуту/ДобавитьСекунду: сдвиг
// внутри суток, которого не давали ДобавитьДень/Месяц/Год. Дробная часть
// аргумента сохраняется (ДобавитьМинуту(д, 0.5) = +30 с) — в отличие от
// ДобавитьДень, где дробь усекается вместе с целочисленным AddDate.
func addTimeBuiltin(unit time.Duration) func([]any, string, int) (any, error) {
	return func(args []any, _ string, _ int) (any, error) {
		t, ok := toTime(args, 0)
		if !ok {
			return nil, nil
		}
		return t.Add(time.Duration(floatArg(args, 1) * float64(unit))), nil
	}
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

// dateConstructor реализует функцию Дата():
//
//	Дата(Год, Месяц, День[, Час, Минута, Секунда])
//	Дата("2026-05-11") / Дата("20260511") / Дата(20260511)
//	Дата(дата) — идемпотентно
//
// Невалидный ввод даёт пустую дату (time.Time{}), как Дата(0) в 1С.
func dateConstructor(args []any, _ string, _ int) (any, error) {
	if len(args) == 1 {
		switch v := args[0].(type) {
		case time.Time:
			return v, nil
		case string:
			if t, ok := parseDateString(v); ok {
				return t, nil
			}
			return time.Time{}, nil
		default:
			if f, ok := toFloat(v); ok && f != 0 {
				if t, ok := parseDateString(strconv.FormatInt(int64(f), 10)); ok {
					return t, nil
				}
			}
			return time.Time{}, nil
		}
	}
	if len(args) < 3 {
		return time.Time{}, nil
	}
	mo := int(floatArg(args, 1))
	d := int(floatArg(args, 2))
	if mo < 1 {
		mo = 1
	}
	if d < 1 {
		d = 1
	}
	return time.Date(int(floatArg(args, 0)), time.Month(mo), d,
		int(floatArg(args, 3)), int(floatArg(args, 4)), int(floatArg(args, 5)),
		0, time.Local), nil
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
