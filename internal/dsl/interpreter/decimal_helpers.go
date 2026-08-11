package interpreter

import (
	"math"

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
	maxDecimalQuotientCoefficientBits       = 16384
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
	case float64:
		return decimal.NewFromFloat(t), true
	case int:
		return decimal.NewFromInt(int64(t)), true
	case int32:
		return decimal.NewFromInt(int64(t)), true
	case int64:
		return decimal.NewFromInt(t), true
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
	case decimal.Decimal, float64, int, int32, int64:
		return true
	}
	return false
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
	d, _ := toDecimal(v)
	return d.IsZero(), true
}
