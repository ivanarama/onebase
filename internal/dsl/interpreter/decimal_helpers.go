package interpreter

import (
	"math"
	"strings"

	"github.com/shopspring/decimal"
)

// divisionPrecision — глобальная точность деления decimal (план 42, решение #1).
// Деление в decimal требует заданной точности, иначе бесконечные дроби (10/3)
// обрезаются непредсказуемо. 16 знаков покрывает типовые учётные расчёты.
const divisionPrecision = 16

// shopspring/decimal при делении, остатке и преобразовании в float64 строит
// промежуточные big.Int с учётом экспоненты. Экспонента Decimal имеет
// int32-диапазон, поэтому синтаксически короткое значение вроде
// `1e2147483647` иначе способно вызвать panic или неконтролируемое выделение
// памяти. Эти границы намного шире типовых учётных значений, но удерживают
// промежуточные числа в десятках килобайт.
const (
	maxDecimalQuotientExponent        int32 = 4096
	maxDecimalQuotientCoefficientBits int   = 16384
	// A coefficient beyond this lexical size cannot fit the bit bound above.
	// Reject it before decimal.NewFromString allocates/parses attacker input.
	maxSandboxDecimalTextBytes = 8192
)

func init() {
	decimal.DivisionPrecision = divisionPrecision
}

// toDecimal приводит DSL-значение к decimal.Decimal.
// Возвращает (Zero, false) для нечислового значения.
// Строки парсятся как числа — это сохраняет прежнюю семантику toFloat,
// где "5" + "3" давало 8 (числовое сложение), а не конкатенацию.
func toDecimal(v any) (decimal.Decimal, bool) {
	switch t := v.(type) {
	case decimal.Decimal:
		return t, true
	case *decimal.Decimal:
		if t == nil {
			return decimal.Zero, false
		}
		return *t, true
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return decimal.Zero, false
		}
		return decimal.NewFromFloat(t), true
	case float32:
		if math.IsNaN(float64(t)) || math.IsInf(float64(t), 0) {
			return decimal.Zero, false
		}
		return decimal.NewFromFloat32(t), true
	case int:
		return decimal.NewFromInt(int64(t)), true
	case int8:
		return decimal.NewFromInt(int64(t)), true
	case int16:
		return decimal.NewFromInt(int64(t)), true
	case int32:
		return decimal.NewFromInt(int64(t)), true
	case int64:
		return decimal.NewFromInt(t), true
	case uint:
		return decimal.NewFromUint64(uint64(t)), true
	case uint8:
		return decimal.NewFromUint64(uint64(t)), true
	case uint16:
		return decimal.NewFromUint64(uint64(t)), true
	case uint32:
		return decimal.NewFromUint64(uint64(t)), true
	case uint64:
		return decimal.NewFromUint64(t), true
	case string:
		if d, err := decimal.NewFromString(t); err == nil {
			return d, true
		}
	}
	return decimal.Zero, false
}

func decimalWithinSafeBounds(d decimal.Decimal) bool {
	exp := d.Exponent()
	if exp < -maxDecimalQuotientExponent || exp > maxDecimalQuotientExponent {
		return false
	}
	return d.Coefficient().BitLen() <= maxDecimalQuotientCoefficientBits
}

// DecimalWithinSafeBounds reports whether decimal operations can expand d
// without constructing an unbounded intermediate big.Int. Sandboxed callers
// that accept data from outside the DSL use the same boundary as division,
// remainder and numeric builtins instead of duplicating subtly different
// exponent/coefficient checks.
func DecimalWithinSafeBounds(d decimal.Decimal) bool {
	return decimalWithinSafeBounds(d)
}

func decimalWithinExpansionBounds(d decimal.Decimal, maxExponent int32) bool {
	if maxExponent <= 0 {
		return true
	}
	exp := d.Exponent()
	if exp < -maxExponent || exp > maxExponent {
		return false
	}
	return d.Coefficient().BitLen() <= maxDecimalQuotientCoefficientBits
}

func requireSafeBuiltinDecimal(operation string, d decimal.Decimal, maxExponent int32, line int) {
	if decimalWithinExpansionBounds(d, maxExponent) {
		return
	}
	panic(userError{
		Msg:  operation + ": число вне безопасного диапазона",
		Line: line,
	})
}

// boundedDecimalPlaces converts the DSL precision argument only after proving
// that decimal.Round cannot rescale by an attacker-controlled int32 distance.
// floatArg used to truncate the value first, which made an out-of-range float
// implementation-dependent and let a small expression request gigabytes of
// zeroes from shopspring/decimal.
func boundedDecimalPlaces(operation string, args []any, index int, maxPlaces int32, line int) int32 {
	if index >= len(args) {
		return 0
	}
	d, ok := toDecimal(args[index])
	if !ok {
		return 0
	}
	requireSafeBuiltinDecimal(operation, d, maxPlaces, line)
	f, ok := decimalToFiniteFloat64(d)
	if !ok || f < -float64(maxPlaces) || f > float64(maxPlaces) {
		panic(userError{
			Msg:  operation + ": точность вне безопасного диапазона",
			Line: line,
		})
	}
	return int32(f)
}

// boundedSandboxBuiltin wraps ordinary builtins only for profiles that opt in
// to MaxDecimalExpansion. The global builtin implementations retain their
// trusted-DSL compatibility, while report/marketplace-style evaluators get a
// fail-closed check at the actual dynamic-argument sink.
func boundedSandboxBuiltin(name string, fn BuiltinFunc, maxDecimalExpansion int32, maxStringExpansion int) BuiltinFunc {
	return func(args []any, file string, line int) (any, error) {
		effectiveDecimalBound := requireSafeSandboxBuiltinInputs(name, args, maxDecimalExpansion, maxStringExpansion, line)

		var result any
		var err error
		formatBound := effectiveDecimalBound
		if maxStringExpansion > 0 && sandboxTemplateBuiltin(name) {
			result = sandboxTemplateBounded(name, args, maxStringExpansion, line)
		} else if formatBound > 0 && (strings.EqualFold(name, "формат") || strings.EqualFold(name, "format")) {
			result, err = fmtBuiltinBounded(args, formatBound)
		} else {
			result, err = fn(args, file, line)
		}
		if err != nil {
			return nil, err
		}
		requireSafeSandboxValueLimits(name, []any{result}, maxDecimalExpansion, maxStringExpansion, line)
		return result, nil
	}
}

func requireSafeSandboxBuiltinInputs(name string, args []any, maxDecimalExpansion int32, maxStringExpansion, line int) int32 {
	// Validate the complete value graph before a string preflight can call fmt
	// on a nested Decimal. Otherwise StrTemplate("%1", hugeDecimal) reaches
	// Decimal.String before the decimal bound gets control.
	requireSafeSandboxValueLimits(name, args, maxDecimalExpansion, maxStringExpansion, line)
	if maxStringExpansion > 0 && sandboxBuiltinFormatsValues(name) {
		requireSafeSandboxFormattingValueLimits(name, args, maxDecimalExpansion, maxStringExpansion, line)
	}
	effectiveDecimalBound := sandboxEffectiveDecimalBound(maxDecimalExpansion, maxStringExpansion)
	requireSafeSandboxDecimalTextArgs(name, args, maxDecimalExpansion, line)
	if maxDecimalExpansion > 0 && (strings.EqualFold(name, "окр") || strings.EqualFold(name, "round")) {
		_ = boundedDecimalPlaces(name, args, 1, maxDecimalExpansion, line)
	}
	if maxStringExpansion > 0 {
		preflightSandboxStringBuiltin(name, args, maxStringExpansion, line)
	}
	return effectiveDecimalBound
}

func sandboxEffectiveDecimalBound(maxDecimal int32, maxString int) int32 {
	bound := maxDecimal
	if maxString <= 0 {
		return bound
	}
	stringBound := int64(maxString)
	if stringBound > int64(^uint32(0)>>1) {
		stringBound = int64(^uint32(0) >> 1)
	}
	if bound <= 0 || stringBound < int64(bound) {
		return int32(stringBound)
	}
	return bound
}

func requireSafeSandboxDecimalTextArgs(operation string, args []any, maxExpansion int32, line int) {
	if maxExpansion <= 0 {
		return
	}
	var indexes []int
	switch strings.ToLower(operation) {
	case "number", "число", "round", "окр", "abs", "абс", "int", "цел",
		"amountinwords", "числопрописью":
		indexes = []int{0}
	case "max", "макс", "min", "мин":
		indexes = []int{0, 1}
	case "sleep", "wait", "пауза", "подождать", "приостановить":
		indexes = []int{0}
	default:
		return
	}
	for _, index := range indexes {
		if index >= len(args) {
			continue
		}
		text, ok := args[index].(string)
		if !ok {
			continue
		}
		requireSafeSandboxDecimalTextLength(operation, text, line)
		if number, err := decimal.NewFromString(text); err == nil {
			requireSafeBuiltinDecimal(operation, number, maxExpansion, line)
		}
	}
}

func requireSafeSandboxDecimalTextLength(operation string, value any, line int) {
	if text, ok := value.(string); ok && len(text) > maxSandboxDecimalTextBytes {
		panic(userError{Msg: operation + ": numeric text exceeds the sandbox decimal limit", Line: line})
	}
}

func requireSafeSandboxNumber(operation string, value any, maxExpansion int32, line int) {
	if !isNumeric(value) {
		return
	}
	if f, ok := value.(float64); ok && (math.IsNaN(f) || math.IsInf(f, 0)) {
		panic(userError{Msg: operation + ": число должно быть конечным", Line: line})
	}
	d, ok := toDecimal(value)
	if !ok {
		panic(userError{Msg: operation + ": число не удалось преобразовать", Line: line})
	}
	requireSafeBuiltinDecimal(operation, d, maxExpansion, line)
}

func requireSafeSandboxDecimalOperation(ec *execCtx, operation string, d decimal.Decimal, line int) {
	if ec == nil || ec.maxDecimalExpansion <= 0 {
		return
	}
	requireSafeBuiltinDecimal(operation, d, ec.maxDecimalExpansion, line)
}

func requireSafeSandboxDecimalOperand(ec *execCtx, operation string, value any, line int) {
	if ec == nil || ec.maxDecimalExpansion <= 0 {
		return
	}
	requireSafeSandboxDecimalTextLength(operation, value, line)
	if number, ok := toDecimal(value); ok {
		requireSafeBuiltinDecimal(operation, number, ec.maxDecimalExpansion, line)
	}
}

func safeSandboxDecimalResult(ec *execCtx, operation string, d decimal.Decimal, line int) decimal.Decimal {
	requireSafeSandboxDecimalOperation(ec, operation, d, line)
	return d
}

func decimalSafeForQuotient(d decimal.Decimal) bool {
	return decimalWithinSafeBounds(d)
}

func requireSafeDecimalQuotient(left, right decimal.Decimal, line int) {
	if decimalSafeForQuotient(left) && decimalSafeForQuotient(right) {
		return
	}
	panic(userError{
		Msg:  "число для деления или остатка вне безопасного диапазона",
		Line: line,
	})
}

// decimalToFiniteFloat64 ограничивает работу shopspring/decimal до вызова
// InexactFloat64. Тот строит big.Int 10^Exponent, поэтому Decimal вида
// 1e2147483647 иначе исчерпывает память в любом builtin, использующем toFloat.
// Границы существенно шире диапазона float64, но удерживают промежуточные
// big.Int в нескольких килобайтах и отсекают бесконечный результат.
func decimalToFiniteFloat64(d decimal.Decimal) (float64, bool) {
	if d.IsZero() {
		return 0, true
	}
	if !decimalWithinSafeBounds(d) {
		return 0, false
	}
	f := d.InexactFloat64()
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// isNumeric сообщает, является ли значение числом (decimal/float/int) без
// парсинга строк. Используется в equal для числового сравнения: строки и даты
// должны и дальше сравниваться через refKey, а не приводиться к числу.
func isNumeric(v any) bool {
	switch v.(type) {
	case decimal.Decimal, *decimal.Decimal, float32, float64,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return true
	}
	return false
}

// nilAsNumericZero подставляет ноль вместо nil, когда вторая сторона сравнения
// — число. Незаполненное числовое поле доезжает до модуля именно как nil: форма
// отдаёт пустую ячейку как nil (typedFormFieldValue), в базе это NULL, и оттуда
// же читается обратно. В 1С пустого значения у числа нет — незаполненное число
// там ноль, — поэтому идиома `Если Стр.Цена = 0` там верна, а здесь молча
// давала Ложь: nil не проходил числовую ветку и сравнивался строково,
// «<nil>» против «0» (#1136).
//
// Правило одностороннее — вторая сторона обязана быть числом: `nil = 5`
// по-прежнему Ложь, `nil = ""` и `nil = nil` сравниваются как раньше.
//
// Зовут его только equalOperator и compareOperator, то есть операторы DSL.
// Поиск по коллекциям (Массив.Найти, ТаблицаЗначений.Найти/НайтиСтроки) и ключи
// Соответствия правило не задевают: первые ходят через compare, вторые через
// refKey, и там «нет значения» остаётся значением, отличным от нуля. Граница
// выбрана так, чтобы отбор незаполненных строк продолжал работать.
//
// Цена правила названа в тесте рядом (nil_numeric_comparison_test.go): признак
// «не найдено» у Найти/Получить — это Неопределено, а `Неопределено = 0` теперь
// Истина, поэтому отличать «не найдено» от «найдено первым» через `= 0` нельзя;
// для этого есть ТипЗнч.
func nilAsNumericZero(a, b any) (any, any) {
	// Ни одна сторона не пуста — самый частый случай, дальше идти незачем.
	if a != nil && b != nil {
		return a, b
	}
	switch {
	case a == nil && comparableNumber(b):
		return decimal.Zero, b
	case b == nil && comparableNumber(a):
		return a, decimal.Zero
	}
	return a, b
}

// comparableNumber — значение, которое сравнение действительно возьмёт числом.
// Одного isNumeric мало: он пропускает нулевой *decimal.Decimal, который
// toDecimal разобрать не может, и подстановка нуля против такого значения
// поменяла бы исход сравнения на ровном месте.
func comparableNumber(v any) bool {
	if !isNumeric(v) {
		return false
	}
	_, ok := toDecimal(v)
	return ok
}

// numericZero сообщает, что значение — числовой ноль. Нужна отдельно от
// сравнения с float64(0)/decimal.Zero, потому что число доезжает до модуля в
// разных Go-типах: из результата запроса на SQLite — int64, из литерала —
// decimal. Строки числами НЕ считаются (в отличие от toDecimal): пустоту строки
// определяет её длина, а не разбор "0" как нуля.
func numericZero(v any) (zero bool, ok bool) {
	if !isNumeric(v) {
		return false, false
	}
	d, ok := toDecimal(v)
	if !ok {
		return false, false
	}
	return d.IsZero(), true
}
