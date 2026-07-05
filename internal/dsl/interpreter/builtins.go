package interpreter

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// BuiltinFunc is a callable value that can be injected via extraVars (e.g. Сообщить).
type BuiltinFunc func(args []any, file string, line int) (any, error)

// DSLError is returned by Error() built-in; stops execution and cancels Save.
type DSLError struct {
	File string
	Line int
	Msg  string
	// Err — исходная ошибка (если есть), например i18nerr с ключом перевода.
	// Unwrap отдаёт её, чтобы i18nerr.Localize смог локализовать сообщение по
	// цепочке (иначе текст сплющивался бы в строку и перевод терялся).
	Err error
}

func (e *DSLError) Error() string {
	if e.File != "" && e.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Msg)
	}
	return e.Msg
}

func (e *DSLError) Unwrap() error { return e.Err }

var builtins = map[string]func(args []any, file string, line int) (any, error){

	// ─── Сообщения ────────────────────────────────────────────────────────
	"сообщить": func(args []any, file string, line int) (any, error) { return nil, nil },
	"message":  func(args []any, file string, line int) (any, error) { return nil, nil },

	// ─── Ошибки ───────────────────────────────────────────────────────────
	"error": func(args []any, file string, line int) (any, error) {
		msg := ""
		if len(args) > 0 {
			msg = fmt.Sprintf("%v", args[0])
		}
		panic(userError{Msg: msg, File: file, Line: line})
	},
	"вызватьисключение": func(args []any, file string, line int) (any, error) {
		msg := ""
		if len(args) > 0 {
			msg = fmt.Sprintf("%v", args[0])
		}
		panic(userError{Msg: msg, File: file, Line: line})
	},

	// ─── Даты ─────────────────────────────────────────────────────────────
	"today": func(args []any, file string, line int) (any, error) {
		t := time.Now()
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local), nil
	},
	"текущаядата": func(args []any, file string, line int) (any, error) {
		t := time.Now()
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local), nil
	},
	"now": func(args []any, file string, line int) (any, error) {
		return time.Now(), nil
	},
	"текущаядатавремя": func(args []any, file string, line int) (any, error) {
		return time.Now(), nil
	},
	"year": func(args []any, file string, line int) (any, error) {
		if t, ok := toTime(args, 0); ok {
			return float64(t.Year()), nil
		}
		return nil, nil
	},
	"год": func(args []any, file string, line int) (any, error) {
		if t, ok := toTime(args, 0); ok {
			return float64(t.Year()), nil
		}
		return nil, nil
	},
	"month": func(args []any, file string, line int) (any, error) {
		if t, ok := toTime(args, 0); ok {
			return float64(t.Month()), nil
		}
		return nil, nil
	},
	"месяц": func(args []any, file string, line int) (any, error) {
		if t, ok := toTime(args, 0); ok {
			return float64(t.Month()), nil
		}
		return nil, nil
	},
	"day": func(args []any, file string, line int) (any, error) {
		if t, ok := toTime(args, 0); ok {
			return float64(t.Day()), nil
		}
		return nil, nil
	},
	"день": func(args []any, file string, line int) (any, error) {
		if t, ok := toTime(args, 0); ok {
			return float64(t.Day()), nil
		}
		return nil, nil
	},

	// ─── Строки ───────────────────────────────────────────────────────────
	"str":    builtinToString,
	"строка": builtinToString,
	"number": builtinToNumber,
	"число":  builtinToNumber,
	"upper": func(args []any, file string, line int) (any, error) {
		return strings.ToUpper(strArg(args, 0)), nil
	},
	"врег": func(args []any, file string, line int) (any, error) {
		return strings.ToUpper(strArg(args, 0)), nil
	},
	"lower": func(args []any, file string, line int) (any, error) {
		return strings.ToLower(strArg(args, 0)), nil
	},
	"нрег": func(args []any, file string, line int) (any, error) {
		return strings.ToLower(strArg(args, 0)), nil
	},
	"trimall": func(args []any, file string, line int) (any, error) {
		return strings.TrimSpace(strArg(args, 0)), nil
	},
	"сокрлп": func(args []any, file string, line int) (any, error) {
		return strings.TrimSpace(strArg(args, 0)), nil
	},
	"left": func(args []any, file string, line int) (any, error) {
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
	"лев": func(args []any, file string, line int) (any, error) {
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
	"right": func(args []any, file string, line int) (any, error) {
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
	"прав": func(args []any, file string, line int) (any, error) {
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
	"mid": func(args []any, file string, line int) (any, error) {
		return midStr(args), nil
	},
	"сред": func(args []any, file string, line int) (any, error) {
		return midStr(args), nil
	},
	"strlen": func(args []any, file string, line int) (any, error) {
		return float64(len([]rune(strArg(args, 0)))), nil
	},
	"стрдлина": func(args []any, file string, line int) (any, error) {
		return float64(len([]rune(strArg(args, 0)))), nil
	},
	"strfind": func(args []any, file string, line int) (any, error) {
		s := strArg(args, 0)
		sub := strArg(args, 1)
		idx := strings.Index(s, sub)
		if idx < 0 {
			return float64(0), nil
		}
		return float64(len([]rune(s[:idx])) + 1), nil
	},
	"стрнайти": func(args []any, file string, line int) (any, error) {
		s := strArg(args, 0)
		sub := strArg(args, 1)
		idx := strings.Index(s, sub)
		if idx < 0 {
			return float64(0), nil
		}
		return float64(len([]rune(s[:idx])) + 1), nil
	},

	// ─── Математика ───────────────────────────────────────────────────────
	"round": builtinRound,
	"окр":   builtinRound,
	"abs":   builtinAbs,
	"абс":   builtinAbs,
	"int":   builtinTrunc,
	"цел":   builtinTrunc,
	"max":   builtinMax,
	"макс":  builtinMax,
	"min":   builtinMin,
	"мин":   builtinMin,

	// ─── JSON ─────────────────────────────────────────────────────────────
	"прочитатьjson": builtinReadJSON,
	"readjson":      builtinReadJSON,
	"записатьjson":  builtinWriteJSON,
	"writejson":     builtinWriteJSON,
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func strArg(args []any, i int) string {
	if i < len(args) && args[i] != nil {
		v := args[i]
		if t, ok := v.(time.Time); ok {
			return t.Format("02.01.2006")
		}
		// Resolved reference (MapThis) — return display name
		if m, ok := v.(*MapThis); ok {
			if name := m.Get("наименование"); name != nil {
				return fmt.Sprintf("%v", name)
			}
			return ""
		}
		return fmt.Sprintf("%v", v)
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

func decimalArg(args []any, i int) decimal.Decimal {
	if i < len(args) {
		if d, ok := toDecimal(args[i]); ok {
			return d
		}
	}
	return decimal.Zero
}

// builtinToString — Строка()/Стр(). Для decimal используем String() напрямую:
// маршрут через toFloat терял бы точность на больших числах.
func builtinToString(args []any, file string, line int) (any, error) {
	if len(args) == 0 {
		return "", nil
	}
	if d, ok := args[0].(decimal.Decimal); ok {
		return d.String(), nil
	}
	if f, ok := toFloat(args[0]); ok {
		if f == math.Trunc(f) {
			return strconv.FormatInt(int64(f), 10), nil
		}
		return strconv.FormatFloat(f, 'f', -1, 64), nil
	}
	if t, ok := args[0].(time.Time); ok {
		return t.Format("02.01.2006 15:04:05"), nil
	}
	// Resolved reference (MapThis) — return its "наименование" field
	if m, ok := args[0].(*MapThis); ok {
		if name := m.Get("наименование"); name != nil {
			return fmt.Sprintf("%v", name), nil
		}
		if name := m.Get("name"); name != nil {
			return fmt.Sprintf("%v", name), nil
		}
		return "", nil
	}
	return fmt.Sprintf("%v", args[0]), nil
}

// builtinToNumber — Число()/Number(). Возвращает decimal, запятая → точка.
func builtinToNumber(args []any, file string, line int) (any, error) {
	if len(args) == 0 {
		return decimal.Zero, nil
	}
	if d, ok := args[0].(decimal.Decimal); ok {
		return d, nil
	}
	s := fmt.Sprintf("%v", args[0])
	d, err := decimal.NewFromString(strings.ReplaceAll(s, ",", "."))
	if err != nil {
		return decimal.Zero, nil
	}
	return d, nil
}

// builtinRound — Окр(Число, Точность, Режим). Режим 0 (по умолчанию) —
// математическое (half away from zero, как Окр в 1С); 1 — банковское.
func builtinRound(args []any, file string, line int) (any, error) {
	d := decimalArg(args, 0)
	places := int32(floatArg(args, 1))
	mode := int(floatArg(args, 2))
	if mode == 1 {
		return d.RoundBank(places), nil
	}
	return d.Round(places), nil
}

func builtinAbs(args []any, file string, line int) (any, error) {
	return decimalArg(args, 0).Abs(), nil
}

func builtinTrunc(args []any, file string, line int) (any, error) {
	return decimalArg(args, 0).Truncate(0), nil
}

func builtinMax(args []any, file string, line int) (any, error) {
	a, b := decimalArg(args, 0), decimalArg(args, 1)
	if a.Cmp(b) >= 0 {
		return a, nil
	}
	return b, nil
}

func builtinMin(args []any, file string, line int) (any, error) {
	a, b := decimalArg(args, 0), decimalArg(args, 1)
	if a.Cmp(b) <= 0 {
		return a, nil
	}
	return b, nil
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
	if start < 0 {
		start = 0
	}
	if start >= len(s) {
		return ""
	}
	// Длина (3-й аргумент) опциональна: без неё Сред возвращает остаток
	// строки до конца — как в 1С:Предприятие.
	end := len(s)
	if len(args) >= 3 {
		end = start + int(floatArg(args, 2))
		if end < start {
			end = start
		}
		if end > len(s) {
			end = len(s)
		}
	}
	return string(s[start:end])
}

// KnownBuiltinNames returns a set of all known callable names (lowercase):
// platform builtins + runtime-injected functions (HTTP, Email, Tx и т.п.).
// Used by the syntax checker to validate function calls in modules.
//
// Имена из фабрик (NewHTTPFunctions, NewEmailFunctions, NewTxFunctions, ...)
// собираются автоматически — добавил builtin в фабрику → имя сразу появилось
// в чек-листе синтаксиса без правок здесь. Ключи с префиксом `__factory_`
// (это служебные конструкторы для СоздатьОбъект, не пользовательские функции)
// исключаются.
//
// Имена, инжектируемые напрямую через buildDSLVars / контекст интерпретатора
// (Сообщить, ОписаниеОшибки, ТекущийПользователь и т.п.), пока перечислены
// явно — у них нет общей фабрики. После выделения dslvars в отдельный пакет
// этот список можно будет заменить на dslvars.Names().
func KnownBuiltinNames() map[string]struct{} {
	names := make(map[string]struct{}, len(builtins)+32)
	for k := range builtins {
		names[k] = struct{}{}
	}
	// special context variables
	names["this"] = struct{}{}
	names["этотобъект"] = struct{}{}

	// автосбор из фабрик. Фабрики вызываются с zero-аргументами — мы только
	// итерируем ключи карты, замыкания не вызываются (некоторые из них при
	// nil-state упадут при реальном вызове, но для перечисления имён это
	// безопасно).
	factoryMaps := []map[string]any{
		NewHTTPFunctions(nil),
		NewEmailFunctions(nil, nil),
		NewTxFunctions(nil, nil),
		NewChartFunctions(),
		NewSpreadsheetFunctions(),
		NewFileFunctions(nil),
		NewLLMFunctions(nil),
		NewServiceFunctions(),
		NewEquipmentFunctions(),
		NewNotifyFunctions(nil),
	}
	for _, m := range factoryMaps {
		for k := range m {
			if strings.HasPrefix(k, "__factory_") {
				continue // конструкторы для СоздатьОбъект, не вызываются по имени
			}
			names[strings.ToLower(k)] = struct{}{}
		}
	}

	// имена, инжектируемые напрямую через buildDSLVars / контекст —
	// без отдельной фабрики (см. ui/handlers.go и scheduler/scheduler.go).
	for _, k := range []string{
		"сообщить", "message",
		"описаниеошибки", "errordescription",
		"информацияобошибке", "errorinfo",
		"вычислить", "eval",
		"блокировкаданных", "datalock",
		"текущийпользователь", "currentuser",
		"имяпользователя", "username",
		"справочники", "catalogs",
		"документы", "documents",
		"регистрынакопления", "accumulationregisters",
		"предопределённыезначения", "predefinedvalues",
		"значениереквизитаобъекта", "objectattributevalue",
		"выполнитькоманду", "executecommand",
		"ссылканаобъект", "objectref",
		"сохранитькартинку", "putimage",
		"добавитьправилооформления", "addformattingrule",
		"очиститьоформление", "clearformatting",
	} {
		names[k] = struct{}{}
	}
	return names
}
