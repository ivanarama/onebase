package interpreter

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// BuiltinFunc is a callable value that can be injected via extraVars (e.g. Сообщить).
type BuiltinFunc func(args []any, file string, line int) (any, error)

// DSLError is returned by Error() built-in; stops execution and cancels Save.
type DSLError struct {
	File string
	Line int
	Msg  string
}

func (e *DSLError) Error() string {
	return fmt.Sprintf("%s:%d: Error: %s", e.File, e.Line, e.Msg)
}

var builtins = map[string]func(args []any, file string, line int) (any, error){

	// ─── Сообщения ────────────────────────────────────────────────────────
	"Сообщить": func(args []any, file string, line int) (any, error) { return nil, nil },
	"Message":  func(args []any, file string, line int) (any, error) { return nil, nil },

	// ─── Ошибки ───────────────────────────────────────────────────────────
	"Error": func(args []any, file string, line int) (any, error) {
		msg := ""
		if len(args) > 0 {
			msg = fmt.Sprintf("%v", args[0])
		}
		return nil, &DSLError{File: file, Line: line, Msg: msg}
	},

	// ─── Даты ─────────────────────────────────────────────────────────────
	// Today() / ТекущаяДата() — текущая дата без времени
	"Today": func(args []any, file string, line int) (any, error) {
		t := time.Now()
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local), nil
	},
	"ТекущаяДата": func(args []any, file string, line int) (any, error) {
		t := time.Now()
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local), nil
	},
	// Now() / ТекущаяДатаВремя() — дата и время
	"Now": func(args []any, file string, line int) (any, error) {
		return time.Now(), nil
	},
	"ТекущаяДатаВремя": func(args []any, file string, line int) (any, error) {
		return time.Now(), nil
	},
	// Year(d) / Год(d)
	"Year": func(args []any, file string, line int) (any, error) {
		if t, ok := toTime(args, 0); ok {
			return float64(t.Year()), nil
		}
		return nil, nil
	},
	"Год": func(args []any, file string, line int) (any, error) {
		if t, ok := toTime(args, 0); ok {
			return float64(t.Year()), nil
		}
		return nil, nil
	},
	// Month(d) / Месяц(d)
	"Month": func(args []any, file string, line int) (any, error) {
		if t, ok := toTime(args, 0); ok {
			return float64(t.Month()), nil
		}
		return nil, nil
	},
	"Месяц": func(args []any, file string, line int) (any, error) {
		if t, ok := toTime(args, 0); ok {
			return float64(t.Month()), nil
		}
		return nil, nil
	},
	// Day(d) / День(d)
	"Day": func(args []any, file string, line int) (any, error) {
		if t, ok := toTime(args, 0); ok {
			return float64(t.Day()), nil
		}
		return nil, nil
	},
	"День": func(args []any, file string, line int) (any, error) {
		if t, ok := toTime(args, 0); ok {
			return float64(t.Day()), nil
		}
		return nil, nil
	},

	// ─── Строки ───────────────────────────────────────────────────────────
	// Str(n) / Строка(n) — число → строка
	"Str": func(args []any, file string, line int) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		if f, ok := toFloat(args[0]); ok {
			if f == math.Trunc(f) {
				return strconv.FormatInt(int64(f), 10), nil
			}
			return strconv.FormatFloat(f, 'f', -1, 64), nil
		}
		return fmt.Sprintf("%v", args[0]), nil
	},
	"Строка": func(args []any, file string, line int) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		if f, ok := toFloat(args[0]); ok {
			if f == math.Trunc(f) {
				return strconv.FormatInt(int64(f), 10), nil
			}
			return strconv.FormatFloat(f, 'f', -1, 64), nil
		}
		return fmt.Sprintf("%v", args[0]), nil
	},
	// Number(s) / Число(s) — строка → число
	"Number": func(args []any, file string, line int) (any, error) {
		if len(args) == 0 {
			return float64(0), nil
		}
		s := fmt.Sprintf("%v", args[0])
		f, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
		if err != nil {
			return float64(0), nil
		}
		return f, nil
	},
	"Число": func(args []any, file string, line int) (any, error) {
		if len(args) == 0 {
			return float64(0), nil
		}
		s := fmt.Sprintf("%v", args[0])
		f, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
		if err != nil {
			return float64(0), nil
		}
		return f, nil
	},
	// Upper(s) / ВРег(s)
	"Upper": func(args []any, file string, line int) (any, error) {
		return strings.ToUpper(strArg(args, 0)), nil
	},
	"ВРег": func(args []any, file string, line int) (any, error) {
		return strings.ToUpper(strArg(args, 0)), nil
	},
	// Lower(s) / НРег(s)
	"Lower": func(args []any, file string, line int) (any, error) {
		return strings.ToLower(strArg(args, 0)), nil
	},
	"НРег": func(args []any, file string, line int) (any, error) {
		return strings.ToLower(strArg(args, 0)), nil
	},
	// TrimAll(s) / СокрЛП(s)
	"TrimAll": func(args []any, file string, line int) (any, error) {
		return strings.TrimSpace(strArg(args, 0)), nil
	},
	"СокрЛП": func(args []any, file string, line int) (any, error) {
		return strings.TrimSpace(strArg(args, 0)), nil
	},
	// Left(s, n) / Лев(s, n)
	"Left": func(args []any, file string, line int) (any, error) {
		s := []rune(strArg(args, 0))
		n := int(floatArg(args, 1))
		if n > len(s) {
			n = len(s)
		}
		if n < 0 {
			n = 0
		}
		return string(s[:n]), nil
	},
	"Лев": func(args []any, file string, line int) (any, error) {
		s := []rune(strArg(args, 0))
		n := int(floatArg(args, 1))
		if n > len(s) {
			n = len(s)
		}
		if n < 0 {
			n = 0
		}
		return string(s[:n]), nil
	},
	// Right(s, n) / Прав(s, n)
	"Right": func(args []any, file string, line int) (any, error) {
		s := []rune(strArg(args, 0))
		n := int(floatArg(args, 1))
		if n > len(s) {
			n = len(s)
		}
		if n < 0 {
			n = 0
		}
		return string(s[len(s)-n:]), nil
	},
	"Прав": func(args []any, file string, line int) (any, error) {
		s := []rune(strArg(args, 0))
		n := int(floatArg(args, 1))
		if n > len(s) {
			n = len(s)
		}
		if n < 0 {
			n = 0
		}
		return string(s[len(s)-n:]), nil
	},
	// Mid(s, start, len) / Сред(s, start, len) — 1-based
	"Mid": func(args []any, file string, line int) (any, error) {
		return midStr(args), nil
	},
	"Сред": func(args []any, file string, line int) (any, error) {
		return midStr(args), nil
	},
	// StrLen(s) / СтрДлина(s)
	"StrLen": func(args []any, file string, line int) (any, error) {
		return float64(len([]rune(strArg(args, 0)))), nil
	},
	"СтрДлина": func(args []any, file string, line int) (any, error) {
		return float64(len([]rune(strArg(args, 0)))), nil
	},
	// StrFind(s, sub) / СтрНайти(s, sub) — 1-based, 0 если не нашли
	"StrFind": func(args []any, file string, line int) (any, error) {
		s := strArg(args, 0)
		sub := strArg(args, 1)
		idx := strings.Index(s, sub)
		if idx < 0 {
			return float64(0), nil
		}
		return float64(len([]rune(s[:idx])) + 1), nil
	},
	"СтрНайти": func(args []any, file string, line int) (any, error) {
		s := strArg(args, 0)
		sub := strArg(args, 1)
		idx := strings.Index(s, sub)
		if idx < 0 {
			return float64(0), nil
		}
		return float64(len([]rune(s[:idx])) + 1), nil
	},

	// ─── Математика ───────────────────────────────────────────────────────
	// Round(n, digits) / Окр(n, digits)
	"Round": func(args []any, file string, line int) (any, error) {
		n := floatArg(args, 0)
		d := int(floatArg(args, 1))
		p := math.Pow(10, float64(d))
		return math.Round(n*p) / p, nil
	},
	"Окр": func(args []any, file string, line int) (any, error) {
		n := floatArg(args, 0)
		d := int(floatArg(args, 1))
		p := math.Pow(10, float64(d))
		return math.Round(n*p) / p, nil
	},
	// Abs(n) / Абс(n)
	"Abs": func(args []any, file string, line int) (any, error) {
		return math.Abs(floatArg(args, 0)), nil
	},
	"Абс": func(args []any, file string, line int) (any, error) {
		return math.Abs(floatArg(args, 0)), nil
	},
	// Int(n) / Цел(n) — целая часть
	"Int": func(args []any, file string, line int) (any, error) {
		return math.Trunc(floatArg(args, 0)), nil
	},
	"Цел": func(args []any, file string, line int) (any, error) {
		return math.Trunc(floatArg(args, 0)), nil
	},
	// Max(a, b) / Макс(a, b)
	"Max": func(args []any, file string, line int) (any, error) {
		return math.Max(floatArg(args, 0), floatArg(args, 1)), nil
	},
	"Макс": func(args []any, file string, line int) (any, error) {
		return math.Max(floatArg(args, 0), floatArg(args, 1)), nil
	},
	// Min(a, b) / Мин(a, b)
	"Min": func(args []any, file string, line int) (any, error) {
		return math.Min(floatArg(args, 0), floatArg(args, 1)), nil
	},
	"Мин": func(args []any, file string, line int) (any, error) {
		return math.Min(floatArg(args, 0), floatArg(args, 1)), nil
	},
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func strArg(args []any, i int) string {
	if i < len(args) && args[i] != nil {
		return fmt.Sprintf("%v", args[i])
	}
	return ""
}

func floatArg(args []any, i int) float64 {
	if i < len(args) {
		if f, ok := toFloat(args[i]); ok {
			return f
		}
	}
	return 0
}

func toTime(args []any, i int) (time.Time, bool) {
	if i < len(args) {
		if t, ok := args[i].(time.Time); ok {
			return t, true
		}
	}
	return time.Time{}, false
}

func midStr(args []any) string {
	s := []rune(strArg(args, 0))
	start := int(floatArg(args, 1)) - 1 // 1-based → 0-based
	length := int(floatArg(args, 2))
	if start < 0 {
		start = 0
	}
	if start >= len(s) {
		return ""
	}
	end := start + length
	if end > len(s) {
		end = len(s)
	}
	return string(s[start:end])
}
