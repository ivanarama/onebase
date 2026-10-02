package query

import "strings"

// ResolveRefOutputColumn сопоставляет объявленное автором имя колонки-ссылки
// (план 182C, id_field у source list-виджета) с фактической колонкой результата
// и подтверждает сущность по RefColumns. Возвращает имя колонки результата или
// пустую строку, если подтверждения нет: голая «Ссылка» компилятор
// переименовывает в фактическую выходную колонку (id), а неоднозначная
// проекция — две ссылки на ту же сущность без явных псевдонимов КАК —
// fail-closed без навигации.
func ResolveRefOutputColumn(res *Result, declared, entity string, cols []string) string {
	if res == nil || declared == "" || entity == "" {
		return ""
	}
	if col := matchOutputColumn(cols, declared); col != "" {
		if ref, ok := res.RefColumns[strings.ToLower(col)]; ok && strings.EqualFold(ref, entity) {
			return col
		}
	}
	lower := strings.ToLower(declared)
	if lower != "ссылка" && !strings.HasSuffix(lower, ".ссылка") {
		return ""
	}
	var matches []string
	for col, ref := range res.RefColumns {
		if !strings.EqualFold(ref, entity) {
			continue
		}
		for _, c := range cols {
			if c == col {
				matches = append(matches, col)
				break
			}
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

func matchOutputColumn(cols []string, declared string) string {
	for _, c := range cols {
		if c == declared {
			return c
		}
	}
	lower := strings.ToLower(declared)
	for _, c := range cols {
		if c == lower {
			return c
		}
	}
	return ""
}
