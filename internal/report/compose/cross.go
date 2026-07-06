package compose

// cross.go — режим кросс-таблицы (pivot): измерения spec.Groupings разворачиваются
// в строки, spec.Columns — в колонки, на пересечении агрегируются spec.Measures.
// Режим включается, когда spec.Columns непуст (диспетчеризация — в обработчике).
// Ядро переиспользует aggregate/aggMeasure/normalizeGroupKey/alignRowKeys обычного
// режима, поэтому вычисляемые показатели (Expr) и регистронезависимость работают
// одинаково.

import (
	"sort"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/report"
)

// CrossCol — колонка кросс-таблицы: путь значений по измерениям spec.Columns
// (для многоуровневых колонок) плюс показатель. При нескольких показателях один
// путь даёт по колонке на каждый показатель.
type CrossCol struct {
	Path    []any  `json:"path"`    // значения по spec.Columns; len == len(spec.Columns)
	Measure string `json:"measure"` // Field показателя (для подписи/формата)
	// MeasureIdx — индекс показателя в spec.Measures. Колонка/ячейка ключуются
	// по индексу, а не по Field (issue #17): два показателя с одинаковым Field,
	// но разным Agg/Expr (например sum(Сумма) и avg(Сумма)) — это РАЗНЫЕ колонки;
	// при ключевании по Field второй затирал бы первый.
	MeasureIdx int `json:"measure_idx"`
}

// Key — стабильный строковый ключ колонки (ключ в CrossRow.Cells / RowTotal).
func (c CrossCol) Key() string { return colKey(c.Path, c.MeasureIdx) }

// CrossRow — узел дерева строковых группировок кросс-таблицы. Cells хранит
// агрегаты показателей по каждой колонке (ключ = CrossCol.Key()). Промежуточный
// узел несёт подытоги по своему поддереву и Children; лист — только Cells.
type CrossRow struct {
	Field    string                      `json:"field"`
	Key      any                         `json:"key"`
	Count    int                         `json:"count"`
	Cells    map[string]any              `json:"cells"`
	Styles   map[string]report.CellStyle `json:"styles,omitempty"` // ключ = CrossCol.Key() → оформление ячейки
	Children []*CrossRow                 `json:"children,omitempty"`
}

// CrossResult — результат компоновки в режиме кросс-таблицы.
type CrossResult struct {
	Columns  []string       `json:"columns"`   // имена измерений-колонок (spec.Columns) — для шапки
	Cols     []CrossCol     `json:"cols"`      // упорядоченные колонки (путь × показатель)
	Rows     []*CrossRow    `json:"rows"`      // дерево строк (верхний уровень)
	RowTotal map[string]any `json:"row_total"` // итог по каждой колонке (ключ = CrossCol.Key())
	RowCount int            `json:"row_count"`
	Capped   bool           `json:"capped"`
	// Warnings — runtime-проблемы компоновки (ошибки вычисляемых показателей и
	// условий оформления), как в обычном своде Result.Warnings (issue #26).
	// Прежде кросс-режим их молча терял. Дедуплицированы.
	Warnings []string `json:"warnings,omitempty"`
}

// ComposeCross строит кросс-таблицу: измерения spec.Groupings — в строки,
// spec.Columns — в колонки, на пересечении — агрегаты spec.Measures.
func ComposeCross(rows []Row, spec report.Composition, ev Evaluator) (*CrossResult, error) {
	return ComposeCrossN(rows, spec, ev, DefaultMaxRows)
}

// ComposeCrossN — ComposeCross с явным потолком строк (как ComposeN).
func ComposeCrossN(rows []Row, spec report.Composition, ev Evaluator, maxRows int) (*CrossResult, error) {
	rows = alignRowKeys(rows, spec)
	res := &CrossResult{Columns: spec.Columns, RowCount: len(rows)}
	if maxRows > 0 && len(rows) > maxRows {
		rows = rows[:maxRows]
		res.Capped = true
		res.RowCount = maxRows
	}
	wc := &warnCollector{}
	res.Cols = crossColumns(rows, spec)
	res.Rows = buildCrossRows(rows, spec, 0, ev, wc)
	res.RowTotal = crossCells(rows, spec, ev, wc)
	res.Warnings = wc.msgs
	return res, nil
}

// colPath собирает путь значений колонки из строки по spec.Columns.
func colPath(r Row, spec report.Composition) []any {
	p := make([]any, len(spec.Columns))
	for i, c := range spec.Columns {
		p[i] = normalizeGroupKey(r[c])
	}
	return p
}

// pathKey — стабильный ключ пути значений колонки.
func pathKey(path []any) string {
	parts := make([]string, len(path))
	for i, p := range path {
		parts[i] = toStr(p)
	}
	return strings.Join(parts, "\x1f")
}

// colKey — ключ ячейки: путь колонки + индекс показателя. В пределах одного
// отчёта len(path) одинаков для всех колонок, поэтому коллизий путь/показатель
// нет. Индекс (а не Field) различает показатели с одинаковым Field, но разным
// агрегатом/выражением (issue #17).
func colKey(path []any, measureIdx int) string {
	return pathKey(path) + "\x1f#" + strconv.Itoa(measureIdx)
}

// comparePaths сравнивает пути колонок поэлементно через compareVals
// (числа как decimal, иначе строки). Более короткий путь считается меньшим.
func comparePaths(a, b []any) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if c := compareVals(a[i], b[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// crossColumns собирает уникальные пути колонок в порядке появления и
// разворачивает их в список CrossCol (путь × каждый показатель).
func crossColumns(rows []Row, spec report.Composition) []CrossCol {
	seen := map[string]bool{}
	var paths [][]any
	for _, r := range rows {
		p := colPath(r, spec)
		k := pathKey(p)
		if !seen[k] {
			seen[k] = true
			paths = append(paths, p)
		}
	}
	// Порядок колонок — по значению пути (числа как decimal, иначе строки),
	// чтобы он не зависел от порядка строк в выборке. Для корректной календарной
	// сортировки период-измерение должно иметь сортируемое значение (дата/число/ISO).
	sort.SliceStable(paths, func(i, j int) bool {
		return comparePaths(paths[i], paths[j]) < 0
	})
	cols := make([]CrossCol, 0, len(paths)*len(spec.Measures))
	for _, p := range paths {
		for mi, m := range spec.Measures {
			cols = append(cols, CrossCol{Path: p, Measure: m.Field, MeasureIdx: mi})
		}
	}
	return cols
}

// buildCrossRows строит дерево строк по spec.Groupings; на каждом узле
// агрегирует показатели по колонкам (crossCells по строкам узла). Сиблинги
// каждого уровня сортируются по spec.Sort (issue #27): прежде строки шли строго
// в порядке выборки, тогда как колонки уже сортировались — несимметрично.
func buildCrossRows(rows []Row, spec report.Composition, level int, ev Evaluator, wc *warnCollector) []*CrossRow {
	if level >= len(spec.Groupings) {
		return nil
	}
	field := spec.Groupings[level]
	var order []any
	buckets := map[any][]Row{}
	for _, r := range rows {
		k := normalizeGroupKey(r[field])
		if _, ok := buckets[k]; !ok {
			order = append(order, k)
		}
		buckets[k] = append(buckets[k], r)
	}
	out := make([]*CrossRow, 0, len(order))
	for _, k := range order {
		cells, styles := crossAgg(buckets[k], spec, ev, wc)
		cr := &CrossRow{
			Field:  field,
			Key:    k,
			Count:  len(buckets[k]),
			Cells:  cells,
			Styles: styles,
		}
		if level+1 < len(spec.Groupings) {
			cr.Children = buildCrossRows(buckets[k], spec, level+1, ev, wc)
		}
		out = append(out, cr)
	}
	sortCrossRows(out, spec)
	return out
}

// sortCrossRows сортирует сиблингов одного уровня дерева по spec.Sort. В
// кросс-таблице у строки нет field-keyed подытогов (ячейки ключуются по колонке),
// поэтому сортировка применяется к ключу группировки строки: правило с
// Field == поле этого уровня (cr.Field) задаёт порядок по cr.Key. Правила по
// другим полям/показателям на данном уровне игнорируются (как в sortGroups,
// где недоступное поле пропускается через continue).
func sortCrossRows(rows []*CrossRow, spec report.Composition) {
	if len(spec.Sort) == 0 || len(rows) == 0 {
		return
	}
	field := rows[0].Field
	sort.SliceStable(rows, func(i, j int) bool {
		for _, sk := range spec.Sort {
			if sk.Field != field {
				continue
			}
			if c := compareVals(rows[i].Key, rows[j].Key); c != 0 {
				if sk.Dir == "desc" {
					return c > 0
				}
				return c < 0
			}
		}
		return false
	})
}

// crossCells агрегирует показатели по каждому пути колонки (без оформления) —
// для итоговой строки RowTotal.
func crossCells(rows []Row, spec report.Composition, ev Evaluator, wc *warnCollector) map[string]any {
	cells, _ := crossAgg(rows, spec, ev, wc)
	return cells
}

// crossAgg агрегирует показатели по каждому пути колонки внутри подмножества
// строк и вычисляет условное оформление ячеек. Вычисляемые показатели (Expr)
// работают так же, как в обычном режиме (через общий aggregate). Стиль ячейки
// (путь, показатель) = правило на этот показатель, иначе правило на всю строку
// (Field "") — по значениям агрегатов данного пути.
//
// Ячейки/стили ключуются по ИНДЕКСУ показателя (issue #17): два показателя с
// одинаковым Field, но разным Agg (sum/avg) дают разные значения и не должны
// схлопываться. Поэтому значение каждого показателя берём независимо: обычные —
// через aggMeasure по индексу, вычисляемые (Expr) — из field-keyed карты
// aggregate (зависимости Expr разрешаются по имени поля, как и раньше).
//
// wc собирает предупреждения исполнения показателей/условий (issue #26): прежде
// кросс-режим терял их (wc=nil) — теперь они прокидываются в CrossResult.Warnings.
func crossAgg(rows []Row, spec report.Composition, ev Evaluator, wc *warnCollector) (map[string]any, map[string]report.CellStyle) {
	byPath := map[string][]Row{}
	paths := map[string][]any{}
	var pathOrder []string
	for _, r := range rows {
		p := colPath(r, spec)
		k := pathKey(p)
		if _, ok := byPath[k]; !ok {
			pathOrder = append(pathOrder, k)
		}
		byPath[k] = append(byPath[k], r)
		paths[k] = p
	}
	cells := map[string]any{}
	var styles map[string]report.CellStyle
	for _, k := range pathOrder {
		sub := byPath[k]
		// agg — field-keyed: служит для вычисления Expr и условий оформления.
		agg := aggregate(sub, spec.Measures, ev, wc)
		st := evalStyles(agg, spec, ev, wc)
		p := paths[k]
		for mi, m := range spec.Measures {
			ck := colKey(p, mi)
			if m.Expr != "" {
				// Вычисляемый показатель: значение уже посчитано в field-keyed agg.
				cells[ck] = agg[m.Field]
			} else {
				// Обычный показатель — считаем независимо по индексу, чтобы
				// показатели с одинаковым Field не затирали друг друга.
				cells[ck] = aggMeasure(sub, m)
			}
			if len(st) == 0 {
				continue
			}
			s, ok := st[m.Field]
			if !ok {
				s, ok = st[""]
			}
			if ok {
				if styles == nil {
					styles = map[string]report.CellStyle{}
				}
				styles[ck] = s
			}
		}
	}
	return cells, styles
}
