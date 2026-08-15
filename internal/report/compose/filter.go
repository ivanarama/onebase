package compose

// Пост-запросная фильтрация строк отчёта по пользовательским отборам (план 70).
// Применяется к строкам результата ДО компоновки — SQL не трогаем (безопаснее
// и не зависит от диалекта). Имена полей сравниваются регистронезависимо, как в
// alignRowKeys (колонки из RunQuery приходят в нижнем регистре).

import (
	"strings"

	"github.com/ivantit66/onebase/internal/report"
)

// ApplyFilters возвращает подмножество rows, удовлетворяющих всем f (AND).
// Числа сравниваются через decimal, строки — лексикографически; contains —
// подстрока без учёта регистра. Пустой список фильтров возвращает rows как есть.
func ApplyFilters(rows []Row, filters []report.Filter) []Row {
	if len(filters) == 0 {
		return rows
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		keep := true
		for _, f := range filters {
			if !matchFilter(r, f) {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, r)
		}
	}
	return out
}

// matchFilter сообщает, проходит ли строка отбор f. Неизвестный оператор →
// фильтр игнорируется (строка проходит). Отсутствие значения по полю → строка
// не проходит сравнение/числовой отбор.
func matchFilter(row Row, f report.Filter) bool {
	op := strings.ToLower(strings.TrimSpace(f.Op))
	raw, found := lookupRowField(row, f.Field)

	switch op {
	case "contains":
		s := ""
		if found {
			s = toStr(raw)
		}
		return strings.Contains(strings.ToLower(s), strings.ToLower(f.Value))
	case "eq", "ne", "gt", "ge", "lt", "le":
		if !found {
			return false
		}
		// Числовое сравнение, если значение строки приводится к числу.
		if d, ok := toDecimal(raw); ok {
			fv, ok2 := toDecimal(f.Value)
			if !ok2 {
				return false // поле число, значение отбора — нет
			}
			return cmpToBool(d.Cmp(fv), op)
		}
		// Иначе строковое сравнение по содержимому.
		return cmpToBool(strings.Compare(toStr(raw), f.Value), op)
	default:
		return true
	}
}

// lookupRowField достаёт значение строки по имени поля регистронезависимо:
// сперва прямое попадание, затем перебор ключей с приведением к нижнему регистру
// (колонки из RunQuery — в нижнем регистре, поле отбора — в исходном).
func lookupRowField(row Row, field string) (any, bool) {
	if v, ok := row[field]; ok {
		return v, true
	}
	lf := strings.ToLower(field)
	for k, v := range row {
		if strings.ToLower(k) == lf {
			return v, true
		}
	}
	return nil, false
}

// cmpToBool интерпретирует результат сравнения (-1/0/1) согласно оператору.
func cmpToBool(cmp int, op string) bool {
	switch op {
	case "eq":
		return cmp == 0
	case "ne":
		return cmp != 0
	case "gt":
		return cmp > 0
	case "ge":
		return cmp >= 0
	case "lt":
		return cmp < 0
	case "le":
		return cmp <= 0
	}
	return false
}
