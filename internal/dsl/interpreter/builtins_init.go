package interpreter

import (
	"fmt"
	"strings"
	"time"
)

// init wires the helpers and ext-functions declared in builtins_helpers.go
// and builtins_ext.go into the global builtins map. Until this init() ran,
// «НачалоМесяца», «Формат», «Пустая» and friends were dead code: defined but
// not callable from DSL.
func init() {
	// ─── Даты: начало/конец периода ───────────────────────────────────────
	begMonth := dateBuiltin(func(t time.Time) any {
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	})
	begYear := dateBuiltin(func(t time.Time) any {
		return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location())
	})
	endYear := dateBuiltin(func(t time.Time) any {
		return time.Date(t.Year(), 12, 31, 23, 59, 59, 0, t.Location())
	})
	begDay := dateBuiltin(func(t time.Time) any {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	})
	endDay := dateBuiltin(func(t time.Time) any {
		return time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, t.Location())
	})

	builtins["началомесяца"] = begMonth
	builtins["begmonth"] = begMonth
	builtins["конецмесяца"] = dateEndMonth
	builtins["endmonth"] = dateEndMonth
	builtins["началогода"] = begYear
	builtins["begyear"] = begYear
	builtins["конецгода"] = endYear
	builtins["endyear"] = endYear
	builtins["началонедели"] = dateBegWeek
	builtins["begweek"] = dateBegWeek
	builtins["конецнедели"] = dateEndWeek
	builtins["endweek"] = dateEndWeek
	builtins["началодня"] = begDay
	builtins["begday"] = begDay
	builtins["конецдня"] = endDay
	builtins["endday"] = endDay
	builtins["добавитьмесяц"] = addMonthBuiltin
	builtins["addmonth"] = addMonthBuiltin
	builtins["добавитьдень"] = addDayBuiltin
	builtins["addday"] = addDayBuiltin
	builtins["добавитьгод"] = addYearBuiltin
	builtins["addyear"] = addYearBuiltin
	// Сдвиг внутри суток. Без него единственным способом было `Дата + N`
	// (N в секундах), а перебор очевидных вариантов вёл в тупик: дробь у
	// ДобавитьДень усекается, Число(Дата) числом не считалось.
	addHour := addTimeBuiltin(time.Hour)
	addMinute := addTimeBuiltin(time.Minute)
	addSecond := addTimeBuiltin(time.Second)
	builtins["добавитьчас"] = addHour
	builtins["добавитьчасов"] = addHour
	builtins["addhour"] = addHour
	builtins["добавитьминуту"] = addMinute
	builtins["добавитьминут"] = addMinute
	builtins["addminute"] = addMinute
	builtins["добавитьсекунду"] = addSecond
	builtins["добавитьсекунд"] = addSecond
	builtins["addsecond"] = addSecond
	builtins["разностьдат"] = dateDiffBuiltin
	builtins["datediff"] = dateDiffBuiltin
	builtins["дата"] = dateConstructor
	builtins["date"] = dateConstructor

	// ─── B2: Час / Минута / Секунда / ДеньНедели ──────────────────────────
	hourFn := dateBuiltin(func(t time.Time) any { return float64(t.Hour()) })
	minuteFn := dateBuiltin(func(t time.Time) any { return float64(t.Minute()) })
	secondFn := dateBuiltin(func(t time.Time) any { return float64(t.Second()) })
	// 1С convention: Пн=1, Вс=7. Go time.Weekday: Sun=0..Sat=6.
	dowFn := dateBuiltin(func(t time.Time) any {
		w := int(t.Weekday())
		return float64(((w + 6) % 7) + 1)
	})
	builtins["час"] = hourFn
	builtins["hour"] = hourFn
	builtins["минута"] = minuteFn
	builtins["minute"] = minuteFn
	builtins["секунда"] = secondFn
	builtins["second"] = secondFn
	builtins["деньнедели"] = dowFn
	builtins["dayofweek"] = dowFn

	// ─── B3: Строковые функции ────────────────────────────────────────────
	chrFn := func(args []any, _ string, _ int) (any, error) {
		return string(rune(int(floatArg(args, 0)))), nil //nolint:gosec // G115: значение приходит из проверенной модели и заведомо укладывается в целевой тип
	}
	builtins["символ"] = chrFn
	builtins["chr"] = chrFn
	builtins["char"] = chrFn

	replaceFn := func(args []any, _ string, _ int) (any, error) {
		return strings.ReplaceAll(strArg(args, 0), strArg(args, 1), strArg(args, 2)), nil
	}
	startsFn := func(args []any, _ string, _ int) (any, error) {
		return strings.HasPrefix(strArg(args, 0), strArg(args, 1)), nil
	}
	endsFn := func(args []any, _ string, _ int) (any, error) {
		return strings.HasSuffix(strArg(args, 0), strArg(args, 1)), nil
	}
	containsFn := func(args []any, _ string, _ int) (any, error) {
		return strings.Contains(strArg(args, 0), strArg(args, 1)), nil
	}

	// СтрРазделить должен возвращать *Array, а не []any — иначе цикл
	// «Для Каждого ... Из» работает, но `arr.Количество()` бросает.
	splitToArray := func(args []any, _ string, _ int) (any, error) {
		parts := strings.Split(strArg(args, 0), strArg(args, 1))
		arr := &Array{}
		for _, p := range parts {
			arr.items = append(arr.items, p)
		}
		return arr, nil
	}

	builtins["стрзаменить"] = replaceFn
	builtins["strreplace"] = replaceFn
	builtins["стрначинаетсяс"] = startsFn
	builtins["strstartswith"] = startsFn
	builtins["стрзаканчиваетсяна"] = endsFn
	builtins["strendswith"] = endsFn
	builtins["стрсодержит"] = containsFn
	builtins["strcontains"] = containsFn
	builtins["стрразделить"] = splitToArray
	builtins["strsplit"] = splitToArray
	builtins["стрсоединить"] = joinBuiltin
	builtins["strjoin"] = joinBuiltin
	builtins["стршаблон"] = templateFromArgs
	builtins["strtemplate"] = templateFromArgs

	// ─── B4: Пустая / ЗначениеЗаполнено ───────────────────────────────────
	emptyFn := func(args []any, _ string, _ int) (any, error) {
		if len(args) == 0 {
			return true, nil
		}
		return isBlankVal(args[0]), nil
	}
	filledFn := func(args []any, _ string, _ int) (any, error) {
		if len(args) == 0 {
			return false, nil
		}
		return !isBlankVal(args[0]), nil
	}
	builtins["пустая"] = emptyFn
	builtins["isblank"] = emptyFn
	builtins["значениезаполнено"] = filledFn
	builtins["isfilled"] = filledFn

	// ПустаяСсылка(x) — узкий предикат именно для ссылок (см.
	// Отличается от Пустая(x) тем, что 0 / Ложь / пустая коллекция → НЕ пустая
	// ссылка. Принимает nil, строку (UUID или ""), *Ref.
	emptyRefFn := func(args []any, _ string, _ int) (any, error) {
		if len(args) == 0 {
			return true, nil
		}
		return isEmptyRefVal(args[0]), nil
	}
	builtins["пустаяссылка"] = emptyRefFn
	builtins["isemptyref"] = emptyRefFn

	// ЧислоПрописью(сумма [, валюта]) — текстовое представление денежной суммы
	// с правильным склонением рубль/рубля/рублей и копейки (
	amountWordsFn := func(args []any, _ string, _ int) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		amount, _ := toDecimal(args[0])
		currency := "rub"
		if len(args) >= 2 {
			if s, ok := args[1].(string); ok && s != "" {
				currency = s
			}
		}
		return AmountInWords(amount, currency), nil
	}
	builtins["числопрописью"] = amountWordsFn
	builtins["amountinwords"] = amountWordsFn

	// Распределить(сумма, веса[, точность]) — пропорциональное распределение
	// с гарантией суммы ( Веса — Массив чисел; результат — Массив
	// той же длины. Точность по умолчанию 2 (копейки).
	distributeFn := func(args []any, _ string, _ int) (any, error) {
		if len(args) < 2 {
			return &Array{}, nil
		}
		total, _ := toFloat(args[0])
		var weights []float64
		switch a := args[1].(type) {
		case *Array:
			for _, item := range a.items {
				f, _ := toFloat(item)
				weights = append(weights, f)
			}
		case []any:
			for _, item := range a {
				f, _ := toFloat(item)
				weights = append(weights, f)
			}
		}
		scale := 2
		if len(args) >= 3 {
			s, _ := toFloat(args[2])
			scale = int(s)
		}
		shares := DistributeAmount(total, weights, scale)
		out := &Array{}
		for _, v := range shares {
			out.items = append(out.items, v)
		}
		return out, nil
	}
	builtins["распределить"] = distributeFn
	builtins["distribute"] = distributeFn

	// ─── B5: Формат ───────────────────────────────────────────────────────
	formatFn := func(args []any, _ string, _ int) (any, error) {
		s, err := fmtBuiltin(args)
		return s, err
	}
	builtins["формат"] = formatFn
	builtins["format"] = formatFn

	// ─── B6: ТипЗнч / Тип ─────────────────────────────────────────────────
	typeOfFn := func(args []any, _ string, _ int) (any, error) {
		if len(args) == 0 {
			return "Неопределено", nil
		}
		return getTypeName(args[0]), nil
	}
	// Тип("Число") — возвращает строку-маркер, чтобы можно было сравнивать с
	// результатом ТипЗнч(). Сама строка нормализуется (Capital case).
	typeFn := func(args []any, _ string, _ int) (any, error) {
		return normalizeTypeName(strArg(args, 0)), nil
	}
	// Приостановить/Пауза/Sleep — блокирующая выдержка (issue #708).
	sleepFn := newSleepBuiltin()
	builtins["приостановить"] = sleepFn
	builtins["пауза"] = sleepFn
	builtins["sleep"] = sleepFn
	builtins["типзнч"] = typeOfFn
	builtins["typeof"] = typeOfFn
	builtins["тип"] = typeFn
	builtins["type"] = typeFn
}

// templateFromArgs is СтрШаблон("Привет, %1!", "Иван") → "Привет, Иван!".
// Replacement order is largest-index first so «%10» doesn't get treated as
// «%1» followed by literal "0".
func templateFromArgs(args []any, _ string, _ int) (any, error) {
	s := strArg(args, 0)
	for i := len(args) - 1; i >= 1; i-- {
		s = strings.ReplaceAll(s, fmt.Sprintf("%%%d", i), fmt.Sprintf("%v", args[i]))
	}
	return s, nil
}

// normalizeTypeName maps user-typed names ("число", "СТРОКА") to canonical form
// returned by getTypeName ("Число", "Строка").
func normalizeTypeName(s string) string {
	low := strings.ToLower(strings.TrimSpace(s))
	switch low {
	case "число", "number":
		return "Число"
	case "строка", "string":
		return "Строка"
	case "булево", "bool", "boolean":
		return "Булево"
	case "дата", "date", "time":
		return "Дата"
	case "массив", "array":
		return "Массив"
	case "структура", "structure":
		return "Структура"
	case "соответствие", "map":
		return "Соответствие"
	case "неопределено", "undefined", "nil":
		return "Неопределено"
	}
	return s
}
