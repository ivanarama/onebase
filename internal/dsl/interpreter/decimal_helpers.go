package interpreter

import "github.com/shopspring/decimal"

// divisionPrecision — глобальная точность деления decimal (план 42, решение #1).
// Деление в decimal требует заданной точности, иначе бесконечные дроби (10/3)
// обрезаются непредсказуемо. 16 знаков покрывает типовые учётные расчёты.
const divisionPrecision = 16

// shopspring/decimal реализует деление и остаток через умножение big.Int на
// 10^разницаЭкспонент. Экспонента Decimal имеет int32-диапазон, поэтому
// синтаксически короткое `1e2147483647 % 1e-2147483648` иначе либо паникует,
// либо пытается выделить гигантское число. Эти границы намного шире типовых
// учётных значений, но удерживают промежуточные числа в десятках килобайт.
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

func decimalSafeForQuotient(d decimal.Decimal) bool {
	exp := d.Exponent()
	if exp < -maxDecimalQuotientExponent || exp > maxDecimalQuotientExponent {
		return false
	}
	return d.Coefficient().BitLen() <= maxDecimalQuotientCoefficientBits
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
