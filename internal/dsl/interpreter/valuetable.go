package interpreter

import (
	"fmt"
	"sort"
	"strings"
)

// ValueTable — in-memory таблица значений (ТаблицаЗначений), аналог типа 1С.
// Строки хранятся как map[ключ_в_нижнем_регистре]значение; наружу строка
// отдаётся как *MapThis, поэтому Стр.Колонка читается/пишется и edits
// отражаются в таблице (общая ссылка на map).
type ValueTable struct {
	columns []string // отображаемые имена колонок; поиск без учёта регистра
	rows    []map[string]any
}

// NewValueTable создаёт пустую ТаблицуЗначений (Новый ТаблицаЗначений).
func NewValueTable(_ []any) *ValueTable { return &ValueTable{} }

func (t *ValueTable) TypeName() string { return "ТаблицаЗначений" }
func (t *ValueTable) String() string {
	return fmt.Sprintf("ТаблицаЗначений[%d]", len(t.rows))
}

func (t *ValueTable) Get(name string) any {
	switch strings.ToLower(name) {
	case "колонки", "columns":
		return &vtColumns{vt: t}
	}
	return nil
}

func (t *ValueTable) Set(_ string, _ any) {}

// IterateRows реализует контракт цикла «Для Каждого» (см. ForEachStmt):
// каждая строка оборачивается в *MapThis.
func (t *ValueTable) IterateRows() []map[string]any { return t.rows }

func (t *ValueTable) colIndex(name string) int {
	low := strings.ToLower(name)
	for i, c := range t.columns {
		if strings.ToLower(c) == low {
			return i
		}
	}
	return -1
}

func (t *ValueTable) addColumn(name string) {
	if name == "" || t.colIndex(name) >= 0 {
		return
	}
	t.columns = append(t.columns, name)
	low := strings.ToLower(name)
	for _, r := range t.rows {
		if _, ok := r[low]; !ok {
			r[low] = nil
		}
	}
}

func (t *ValueTable) addRow() map[string]any {
	r := make(map[string]any, len(t.columns))
	for _, c := range t.columns {
		r[strings.ToLower(c)] = nil
	}
	t.rows = append(t.rows, r)
	return r
}

func (t *ValueTable) CallMethod(name string, args []any) any {
	switch strings.ToLower(name) {
	case "колонки", "columns":
		return &vtColumns{vt: t}
	case "добавить", "add":
		return &MapThis{M: t.addRow()}
	case "количество", "count":
		return float64(len(t.rows))
	case "получить", "get":
		idx := int(floatArg(args, 0))
		if idx >= 0 && idx < len(t.rows) {
			return &MapThis{M: t.rows[idx]}
		}
		return nil
	case "удалить", "delete":
		idx := int(floatArg(args, 0))
		if idx >= 0 && idx < len(t.rows) {
			t.rows = append(t.rows[:idx], t.rows[idx+1:]...)
		}
	case "очистить", "clear":
		t.rows = nil
	case "итог", "total":
		col := strings.ToLower(strArg(args, 0))
		var sum float64
		for _, r := range t.rows {
			if f, ok := toFloat(r[col]); ok {
				sum += f
			}
		}
		return sum
	case "выгрузитьколонку", "unloadcolumn":
		col := strings.ToLower(strArg(args, 0))
		arr := &Array{}
		for _, r := range t.rows {
			arr.items = append(arr.items, r[col])
		}
		return arr
	case "загрузитьколонку", "loadcolumn":
		return t.loadColumn(args)
	case "найти", "find":
		return t.find(args)
	case "найтистроки", "findrows":
		return t.findRows(args)
	case "сортировать", "sort":
		t.sortRows(strArg(args, 0))
	case "свернуть", "collapse":
		t.collapse(strArg(args, 0), strArg(args, 1))
	}
	return nil
}

func (t *ValueTable) loadColumn(args []any) any {
	var vals []any
	switch a := args[0].(type) {
	case *Array:
		vals = a.items
	case []any:
		vals = a
	}
	colName := strArg(args, 1)
	t.addColumn(colName)
	low := strings.ToLower(colName)
	for i, r := range t.rows {
		if i < len(vals) {
			r[low] = vals[i]
		}
	}
	return nil
}

// find — Найти(Значение[, Колонка]). Возвращает первую подходящую строку
// (*MapThis) или Неопределено (nil). Без указания колонки ищет по всем.
func (t *ValueTable) find(args []any) any {
	if len(args) == 0 {
		return nil
	}
	val := args[0]
	var cols []string
	if c := strings.ToLower(strArg(args, 1)); len(args) >= 2 && c != "" {
		cols = []string{c}
	} else {
		for _, c := range t.columns {
			cols = append(cols, strings.ToLower(c))
		}
	}
	for _, r := range t.rows {
		for _, c := range cols {
			if compareAny(r[c], val) == 0 {
				return &MapThis{M: r}
			}
		}
	}
	return nil
}

// findRows — НайтиСтроки(ОтборСтруктура). Возвращает Массив строк (*MapThis),
// у которых все поля отбора совпадают.
func (t *ValueTable) findRows(args []any) any {
	out := &Array{}
	filt, ok := args[0].(*Struct)
	if !ok {
		return out
	}
	for _, r := range t.rows {
		match := true
		for _, k := range filt.keys {
			if compareAny(r[k], filt.vals[k]) != 0 {
				match = false
				break
			}
		}
		if match {
			out.items = append(out.items, &MapThis{M: r})
		}
	}
	return out
}

// sortRows — Сортировать("Колонка1 Убыв, Колонка2"). Стабильная многоключевая
// сортировка; направление по умолчанию — по возрастанию.
func (t *ValueTable) sortRows(spec string) {
	type key struct {
		col  string
		desc bool
	}
	var keys []key
	for _, part := range splitComma(spec) {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		k := key{col: strings.ToLower(fields[0])}
		if len(fields) >= 2 {
			d := strings.ToLower(fields[1])
			k.desc = d == "убыв" || d == "desc"
		}
		keys = append(keys, k)
	}
	sort.SliceStable(t.rows, func(i, j int) bool {
		for _, k := range keys {
			c := compareAny(t.rows[i][k.col], t.rows[j][k.col])
			if c == 0 {
				continue
			}
			if k.desc {
				return c > 0
			}
			return c < 0
		}
		return false
	})
}

// collapse — Свернуть("КолонкиГруппировки", "КолонкиСуммирования"): группирует
// строки по группировочным колонкам, суммируя числовые колонки суммирования.
func (t *ValueTable) collapse(groupSpec, sumSpec string) {
	groupCols := splitComma(groupSpec)
	sumCols := splitComma(sumSpec)

	order := []string{}
	groups := map[string]map[string]any{}
	for _, r := range t.rows {
		var kb strings.Builder
		for _, gc := range groupCols {
			fmt.Fprintf(&kb, "%v\x00", r[strings.ToLower(gc)])
		}
		gk := kb.String()
		g, ok := groups[gk]
		if !ok {
			g = map[string]any{}
			for _, gc := range groupCols {
				g[strings.ToLower(gc)] = r[strings.ToLower(gc)]
			}
			for _, sc := range sumCols {
				g[strings.ToLower(sc)] = float64(0)
			}
			groups[gk] = g
			order = append(order, gk)
		}
		for _, sc := range sumCols {
			low := strings.ToLower(sc)
			cur, _ := toFloat(g[low])
			add, _ := toFloat(r[low])
			g[low] = cur + add
		}
	}

	t.rows = t.rows[:0]
	for _, gk := range order {
		t.rows = append(t.rows, groups[gk])
	}
	t.columns = append(append([]string{}, groupCols...), sumCols...)
}

// ─── Колонки ────────────────────────────────────────────────────────────────

type vtColumns struct{ vt *ValueTable }

func (c *vtColumns) TypeName() string { return "КолонкиТаблицыЗначений" }

func (c *vtColumns) CallMethod(name string, args []any) any {
	switch strings.ToLower(name) {
	case "добавить", "add":
		// Добавить(Имя[, Тип]) — тип игнорируется (типизация динамическая).
		c.vt.addColumn(strArg(args, 0))
	case "количество", "count":
		return float64(len(c.vt.columns))
	case "получить", "get":
		idx := int(floatArg(args, 0))
		if idx >= 0 && idx < len(c.vt.columns) {
			return c.vt.columns[idx]
		}
		return nil
	case "удалить", "delete":
		if idx := c.vt.colIndex(strArg(args, 0)); idx >= 0 {
			c.vt.columns = append(c.vt.columns[:idx], c.vt.columns[idx+1:]...)
		}
	}
	return nil
}

// compareAny сравнивает значения для сортировки/поиска: числа точно как
// decimal, даты по времени, остальное — как строки. Возвращает -1/0/1.
func compareAny(a, b any) int {
	// Используем тот же comparator, что у операторов <, >, <= и >=. Старый
	// путь через float64 склеивал разные Decimal за границей 2^53, и стабильная
	// сортировка оставляла их в исходном (в том числе неверном) порядке.
	return compare(a, b)
}
