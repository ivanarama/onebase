package interpreter

import (
	"fmt"
	"unicode/utf8"
)

const (
	maxLevenshteinInputBytes = 4 << 20
	maxLevenshteinCells      = 1_000_000
)

func init() {
	builtins["расстояниелевенштейна"] = levenshteinBuiltin
	builtins["levenshteindistance"] = levenshteinBuiltin
}

// Distance uses Unicode code points with unit insertion/deletion/substitution
// costs. Keep only the shorter input and two DP rows: O(min(n, m)) memory.
func levenshteinBuiltin(args []any, _ string, _ int) (any, error) {
	if len(args) != 2 {
		RaiseUserError("РасстояниеЛевенштейна: ожидаются две строки")
	}
	a, okA := args[0].(string)
	b, okB := args[1].(string)
	if !okA || !okB {
		RaiseUserError("РасстояниеЛевенштейна: оба аргумента должны быть строками")
	}
	if len(a) > maxLevenshteinInputBytes || len(b) > maxLevenshteinInputBytes {
		RaiseUserError(fmt.Sprintf("РасстояниеЛевенштейна: длина каждой строки не должна превышать %d байт", maxLevenshteinInputBytes))
	}
	n, m := utf8.RuneCountInString(a), utf8.RuneCountInString(b)
	if n < m {
		a, b = b, a
		n, m = m, n
	}
	if m == 0 {
		return float64(n), nil
	}
	// Division avoids overflowing n*m before checking the work budget.
	if n > maxLevenshteinCells/m {
		RaiseUserError(fmt.Sprintf("РасстояниеЛевенштейна: произведение длин строк в символах превышает предел вычислений %d", maxLevenshteinCells))
	}
	short := []rune(b)
	previous, current := make([]int, m+1), make([]int, m+1)
	for j := range previous {
		previous[j] = j
	}
	i := 0
	for _, left := range a {
		i++
		current[0] = i
		for j, right := range short {
			cost := 1
			if left == right {
				cost = 0
			}
			current[j+1] = min(previous[j+1]+1, current[j]+1, previous[j]+cost)
		}
		previous, current = current, previous
	}
	return float64(previous[m]), nil
}
