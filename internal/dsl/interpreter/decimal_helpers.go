package interpreter

import "github.com/shopspring/decimal"

// divisionPrecision — глобальная точность деления decimal (план 42, решение #1).
// Деление в decimal требует заданной точности, иначе бесконечные дроби (10/3)
// обрезаются непредсказуемо. 16 знаков покрывает типовые учётные расчёты.
const divisionPrecision = 16

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

// numericOrBool — числовое значение для сравнения на равенство: числа как есть,
// булево как 1/0. Одно и то же булево поле приходит в модуль разными типами —
// PostgreSQL хранит булево как bool, SQLite как INTEGER 0/1, — поэтому без
// приведения `Стр.Флаг = Истина` работал бы на PostgreSQL и молча не
// срабатывал на SQLite (issue #704).
//
// Строки сюда не попадают: разбор "0"/"true" из строки сделал бы равными
// значения, которые пользователь сравнивает как текст.
func numericOrBool(v any) (decimal.Decimal, bool) {
	if b, ok := v.(bool); ok {
		if b {
			return decimal.NewFromInt(1), true
		}
		return decimal.Zero, true
	}
	if !isNumeric(v) {
		return decimal.Zero, false
	}
	return toDecimal(v)
}
