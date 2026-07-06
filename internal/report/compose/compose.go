// Package compose сворачивает плоские строки отчёта в дерево групп с итогами.
// Чистый слой: без БД/HTTP. Условия оформления вычисляются через Evaluator.
package compose

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/token"
	"github.com/ivantit66/onebase/internal/report"
)

type Row = map[string]any

// Evaluator вычисляет DSL-выражения при значениях полей строки.
type Evaluator interface {
	EvalBool(expr string, row Row) (bool, error)
	// EvalNum вычисляет числовое выражение (для вычисляемых показателей).
	// Возвращает (значение, удалось, ошибка). При ok=false результат не используется.
	EvalNum(expr string, row Row) (decimal.Decimal, bool, error)
}

const DefaultMaxRows = 50000

type Result struct {
	Columns  []string       `json:"columns"`
	Groups   []*Group       `json:"groups"`
	Grand    map[string]any `json:"grand"`
	RowCount int            `json:"row_count"`
	Capped   bool           `json:"capped"`
	// Warnings — runtime-проблемы компоновки (ошибки исполнения вычисляемых
	// показателей и условий оформления, циклы зависимостей). Дедуплицированы:
	// одна и та же ошибка повторяется для каждой группы/итога. Пусто — всё ок.
	Warnings []string `json:"warnings,omitempty"`
}

// warnCollector копит дедуплицированные предупреждения компоновки. Одно и то же
// выражение вычисляется для каждой группы/подытога/итога — без дедупликации
// предупреждение повторялось бы десятки раз. nil-приёмник безопасен (no-op).
type warnCollector struct {
	seen map[string]bool
	msgs []string
}

func (w *warnCollector) add(msg string) {
	if w == nil {
		return
	}
	if w.seen == nil {
		w.seen = map[string]bool{}
	}
	if w.seen[msg] {
		return
	}
	w.seen[msg] = true
	w.msgs = append(w.msgs, msg)
}

type Group struct {
	Field     string                      `json:"field"`
	Key       any                         `json:"key"`
	Subtotals map[string]any              `json:"subtotals"`
	Count     int                         `json:"count"`
	Children  []*Group                    `json:"children,omitempty"`
	Details   []DetailRow                 `json:"details,omitempty"`
	Styles    map[string]report.CellStyle `json:"styles,omitempty"` // условное оформление по подытогам; ключ "" = вся строка
}

type DetailRow struct {
	Values map[string]any              `json:"values"`
	Styles map[string]report.CellStyle `json:"styles,omitempty"` // ключ "" = стиль на всю строку
}

func Compose(rows []Row, spec report.Composition, ev Evaluator) (*Result, error) {
	return ComposeN(rows, spec, ev, DefaultMaxRows)
}

func ComposeN(rows []Row, spec report.Composition, ev Evaluator, maxRows int) (*Result, error) {
	rows = alignRowKeys(rows, spec)
	res := &Result{RowCount: len(rows)}
	if maxRows > 0 && len(rows) > maxRows {
		rows = rows[:maxRows]
		res.Capped = true
		res.RowCount = maxRows
	}
	wc := &warnCollector{}
	res.Groups = buildGroups(rows, spec, 0, ev, wc)
	res.Grand = aggregate(rows, spec.Measures, ev, wc)
	res.Warnings = wc.msgs
	return res, nil
}

// alignRowKeys добавляет каждой строке ключи под именами полей из spec, находя
// соответствующие колонки регистронезависимо. Компилятор запросов отдаёт имена
// колонок в нижнем регистре (ВЫБРАТЬ Выручка КАК Выручка → колонка "выручка"),
// тогда как composition ссылается на поля в исходном регистре; без выравнивания
// доступ row[field] не находил бы данные и все строки схлопывались бы в одну
// пустую группу с нулевыми итогами. Исходные ключи сохраняются. Согласуется с
// тем, что DSL OneBase регистронезависим.
func alignRowKeys(rows []Row, spec report.Composition) []Row {
	targets := map[string]bool{}
	add := func(s string) {
		if s != "" {
			targets[s] = true
		}
	}
	for _, g := range spec.Groupings {
		add(g)
	}
	for _, c := range spec.Columns {
		add(c)
	}
	for _, m := range spec.Measures {
		add(m.Field)
	}
	for _, s := range spec.Sort {
		add(s.Field)
	}
	for _, c := range spec.Conditional {
		add(c.Field)
	}
	if spec.Chart != nil {
		add(spec.Chart.Category)
		for _, s := range spec.Chart.Series {
			add(s)
		}
	}
	// DetailLink — колонка строки, из которой берётся UUID для ссылки на документ.
	// DetailEntity — имя сущности (не поле строки), в alignRowKeys не добавляем.
	add(spec.DetailLink)
	if len(targets) == 0 {
		return rows
	}
	byLower := make(map[string]string, len(targets))
	for t := range targets {
		byLower[strings.ToLower(t)] = t
	}
	out := make([]Row, len(rows))
	for i, r := range rows {
		nr := make(Row, len(r)+len(targets))
		for k, v := range r {
			nr[k] = v
			if t, ok := byLower[strings.ToLower(k)]; ok && t != k {
				nr[t] = v
			}
		}
		out[i] = nr
	}
	return out
}

func buildGroups(rows []Row, spec report.Composition, level int, ev Evaluator, wc *warnCollector) []*Group {
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
	groups := make([]*Group, 0, len(order))
	for _, k := range order {
		gr := &Group{
			Field:     field,
			Key:       k,
			Count:     len(buckets[k]),
			Subtotals: aggregate(buckets[k], spec.Measures, ev, wc),
		}
		// Условное оформление группы/подытога вычисляется по её подытогам плюс
		// собственному полю группировки (field→Key) — чтобы правило вида
		// «Регион = "Юг"» подсвечивало и итоговую строку группы, а не только
		// detail-строки (раньше итогу было доступно только множество мер).
		styleRow := make(Row, len(gr.Subtotals)+1)
		for k, v := range gr.Subtotals {
			styleRow[k] = v
		}
		styleRow[field] = gr.Key
		gr.Styles = evalStyles(styleRow, spec, ev, wc)
		if level+1 < len(spec.Groupings) {
			gr.Children = buildGroups(buckets[k], spec, level+1, ev, wc)
		} else if spec.Detail {
			gr.Details = buildDetails(buckets[k], spec, ev, wc)
		}
		groups = append(groups, gr)
	}
	sortGroups(groups, spec)
	return groups
}

// evalStyles вычисляет условное оформление для строки значений (детальной или
// подытога группы): первое сработавшее правило на целевое поле задаёт стиль.
func evalStyles(row Row, spec report.Composition, ev Evaluator, wc *warnCollector) map[string]report.CellStyle {
	if len(spec.Conditional) == 0 || ev == nil {
		return nil
	}
	var styles map[string]report.CellStyle
	for _, rule := range spec.Conditional {
		if _, done := styles[rule.Field]; done {
			continue // первое сработавшее правило на целевое поле
		}
		ok, err := ev.EvalBool(rule.When, row)
		if err != nil {
			// Runtime-ошибка условия — это не «правило не сработало»: показываем
			// её, а не молча гасим оформление (отличаем от обычного false).
			wc.add(fmt.Sprintf("условие оформления «%s»: %v", rule.When, err))
			continue
		}
		if !ok {
			continue
		}
		if styles == nil {
			styles = map[string]report.CellStyle{}
		}
		styles[rule.Field] = rule.Style
	}
	return styles
}

func buildDetails(rows []Row, spec report.Composition, ev Evaluator, wc *warnCollector) []DetailRow {
	out := make([]DetailRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, DetailRow{Values: r, Styles: evalStyles(r, spec, ev, wc)})
	}
	sortDetails(out, spec)
	return out
}

func measureSet(spec report.Composition) map[string]bool {
	m := map[string]bool{}
	for _, x := range spec.Measures {
		m[x.Field] = true
	}
	return m
}

func sortGroups(groups []*Group, spec report.Composition) {
	if len(spec.Sort) == 0 {
		return
	}
	meas := measureSet(spec)
	sort.SliceStable(groups, func(i, j int) bool {
		for _, sk := range spec.Sort {
			var vi, vj any
			switch {
			case meas[sk.Field]:
				vi, vj = groups[i].Subtotals[sk.Field], groups[j].Subtotals[sk.Field]
			case sk.Field == groups[i].Field:
				vi, vj = groups[i].Key, groups[j].Key
			default:
				continue
			}
			if c := compareVals(vi, vj); c != 0 {
				if sk.Dir == "desc" {
					return c > 0
				}
				return c < 0
			}
		}
		return false
	})
}

func sortDetails(rows []DetailRow, spec report.Composition) {
	if len(spec.Sort) == 0 {
		return
	}
	sort.SliceStable(rows, func(i, j int) bool {
		for _, sk := range spec.Sort {
			if c := compareVals(rows[i].Values[sk.Field], rows[j].Values[sk.Field]); c != 0 {
				if sk.Dir == "desc" {
					return c > 0
				}
				return c < 0
			}
		}
		return false
	})
}

// compareVals: -1/0/1. Числа сравниваются как decimal, иначе как строки.
// Если одно значение числовое, а другое нет (nil-подытог пустой группы,
// нечисловой текст) — числовое считается «меньше», то есть непарсимое уходит
// в конец при сортировке по возрастанию, а не сравнивается лексикографически
// с числом (issue #90).
func compareVals(a, b any) int {
	da, oka := toDecimal(a)
	db, okb := toDecimal(b)
	switch {
	case oka && okb:
		return da.Cmp(db)
	case oka:
		return -1
	case okb:
		return 1
	}
	sa, sb := toStr(a), toStr(b)
	switch {
	case sa < sb:
		return -1
	case sa > sb:
		return 1
	default:
		return 0
	}
}

func toStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// normalizeGroupKey приводит значение группировки к надёжному ключу map.
// Числа любого типа (int/int64/float64/decimal) канонизируются в одну строку —
// иначе равные по значению ключи разных типов попали бы в разные группы, а
// decimal.Decimal как ключ map сравнивался бы по указателю big.Int. []byte
// (его может вернуть драйвер БД) неэшируем как ключ map — приводим к строке,
// иначе buildGroups паникует. compareVals при сортировке всё равно распарсит
// числовую строку обратно в decimal, так что числовой порядок сохраняется.
func normalizeGroupKey(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(x)
	case decimal.Decimal:
		return x.String()
	case int:
		return decimal.NewFromInt(int64(x)).String()
	case int64:
		return decimal.NewFromInt(x).String()
	case float64:
		// NaN/±Inf нельзя превратить в decimal — decimal.NewFromFloat паникует
		// (issue #9). Такие значения приходят из REAL/float8 колонок; ключуем их
		// строкой, чтобы группировка не падала. compareVals при сортировке вернёт
		// их в строковую ветку (числом они не распарсятся).
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return strconv.FormatFloat(x, 'g', -1, 64)
		}
		return decimal.NewFromFloat(x).String()
	}
	return v
}

// GroupByField сворачивает строки по одному произвольному полю и агрегирует
// меры — для диаграмм, где ось категорий (Chart.Category) не совпадает с верхней
// группировкой отчёта. Возвращает ключи в порядке появления и подытоги по ключу.
// Строки выравниваются по spec (alignRowKeys) — поэтому field и поля мер
// находятся регистронезависимо, как и в основном своде.
func GroupByField(rows []Row, spec report.Composition, field string, ev Evaluator) (keys []any, subtotals map[any]map[string]any) {
	rows = alignRowKeys(rows, spec)
	buckets := map[any][]Row{}
	for _, r := range rows {
		k := normalizeGroupKey(r[field])
		if _, ok := buckets[k]; !ok {
			keys = append(keys, k)
		}
		buckets[k] = append(buckets[k], r)
	}
	subtotals = make(map[any]map[string]any, len(buckets))
	for k, rs := range buckets {
		// Предупреждения уже собраны основным проходом Compose — здесь nil-приёмник.
		subtotals[k] = aggregate(rs, spec.Measures, ev, nil)
	}
	return keys, subtotals
}

func aggregate(rows []Row, measures []report.Measure, ev Evaluator, wc *warnCollector) map[string]any {
	out := map[string]any{}
	// Первый проход: обычные показатели (не вычисляемые).
	for _, m := range measures {
		if m.Expr != "" {
			continue // вычисляемые — во второй проход
		}
		out[m.Field] = aggMeasure(rows, m)
	}
	// Второй проход: вычисляемые показатели. Порядок определяется зависимостями
	// (Expr, ссылающийся на другой Expr, вычисляется после него) — порядок
	// объявления в YAML больше не критичен. Циклы разрываются с предупреждением.
	order, cyclic := orderExprMeasures(measures)
	if len(cyclic) > 0 {
		names := make([]string, len(cyclic))
		for i, idx := range cyclic {
			names[i] = measures[idx].Field
		}
		wc.add("циклическая зависимость вычисляемых показателей: " + strings.Join(names, ", "))
	}
	for _, idx := range order {
		m := measures[idx]
		if ev == nil {
			out[m.Field] = nil
			continue
		}
		d, ok, err := ev.EvalNum(m.Expr, out)
		switch {
		case err != nil:
			// Runtime-ошибка (не синтаксис — тот ловит onebase check): показываем
			// её и оставляем ячейку пустой, не выдавая за «неопределённое значение».
			wc.add(fmt.Sprintf("показатель «%s»: %v", m.Field, err))
			out[m.Field] = nil
		case ok:
			out[m.Field] = d
		default:
			// ok=false без ошибки — неопределённое значение (например деление на
			// ноль) → пустая ячейка, как в 1С (без предупреждения).
			out[m.Field] = nil
		}
	}
	return out
}

// orderExprMeasures возвращает индексы вычисляемых (Expr) показателей в порядке,
// при котором каждый идёт после тех Expr-показателей, на которые он ссылается
// (топологическая сортировка, стабильная — при отсутствии зависимостей сохраняет
// порядок объявления). cyclic — индексы показателей, оставшихся в цикле; они
// добавляются в конец order в порядке объявления (вычислятся «как есть»).
func orderExprMeasures(measures []report.Measure) (order []int, cyclic []int) {
	// Имена приводим к нижнему регистру: интерпретатор разрешает идентификаторы
	// регистронезависимо, поэтому и зависимости между Expr должны определяться так.
	exprFields := map[string]bool{}
	var idxs []int
	for i, m := range measures {
		if m.Expr != "" {
			exprFields[strings.ToLower(m.Field)] = true
			idxs = append(idxs, i)
		}
	}
	if len(idxs) <= 1 {
		return idxs, nil
	}
	deps := make(map[int]map[string]bool, len(idxs))
	for _, i := range idxs {
		deps[i] = exprIdentDeps(measures[i].Expr, exprFields, strings.ToLower(measures[i].Field))
	}
	resolved := map[string]bool{}
	remaining := append([]int(nil), idxs...)
	for len(remaining) > 0 {
		var next []int
		progressed := false
		for _, i := range remaining {
			ready := true
			for d := range deps[i] {
				if !resolved[d] {
					ready = false
					break
				}
			}
			if ready {
				order = append(order, i)
				resolved[strings.ToLower(measures[i].Field)] = true
				progressed = true
			} else {
				next = append(next, i)
			}
		}
		if !progressed {
			// Цикл: оставшиеся неразрешимы — выводим в порядке объявления.
			order = append(order, next...)
			return order, next
		}
		remaining = next
	}
	return order, nil
}

// exprIdentDeps возвращает множество имён ДРУГИХ вычисляемых показателей
// (exprFields, ключи в нижнем регистре), на которые ссылается выражение expr.
// Используем лексер DSL, чтобы брать идентификаторы по-токенно (а не подстрокой),
// сравниваем регистронезависимо (как интерпретатор) и исключаем сам показатель
// (self уже в нижнем регистре) — самоссылка не создаёт зависимости.
func exprIdentDeps(expr string, exprFields map[string]bool, self string) map[string]bool {
	deps := map[string]bool{}
	lx := lexer.New(expr, "")
	for {
		t := lx.NextToken()
		if t.Type == token.EOF {
			break
		}
		if t.Type != token.IDENT {
			continue
		}
		lit := strings.ToLower(t.Literal)
		if lit != self && exprFields[lit] {
			deps[lit] = true
		}
	}
	return deps
}

func aggMeasure(rows []Row, m report.Measure) any {
	if m.Agg == "count" {
		return int64(len(rows))
	}
	var acc, mn, mx decimal.Decimal
	cnt := 0
	first := true
	for _, r := range rows {
		d, ok := toDecimal(r[m.Field])
		if !ok {
			continue
		}
		cnt++
		acc = acc.Add(d)
		if first {
			mn, mx = d, d
			first = false
		} else {
			if d.LessThan(mn) {
				mn = d
			}
			if d.GreaterThan(mx) {
				mx = d
			}
		}
	}
	switch m.Agg {
	case "min":
		if first {
			return nil
		}
		return mn
	case "max":
		if first {
			return nil
		}
		return mx
	case "avg":
		if cnt == 0 {
			return nil
		}
		return acc.Div(decimal.NewFromInt(int64(cnt)))
	default: // sum / ""
		return acc
	}
}

// ExportToDecimal — toDecimal для внешних пакетов (ui-рендер графика).
func ExportToDecimal(v any) (decimal.Decimal, bool) { return toDecimal(v) }

func toDecimal(v any) (decimal.Decimal, bool) {
	switch x := v.(type) {
	case decimal.Decimal:
		return x, true
	case int:
		return decimal.NewFromInt(int64(x)), true
	case int64:
		return decimal.NewFromInt(x), true
	case float64:
		// NaN/±Inf вне области decimal — decimal.NewFromFloat паникует (issue #9).
		// Трактуем как «не число»: значение выпадает из сумм/средних/сортировки,
		// а не валит весь отчёт паникой.
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return decimal.Zero, false
		}
		return decimal.NewFromFloat(x), true
	case string:
		d, err := decimal.NewFromString(x)
		if err != nil {
			return decimal.Zero, false
		}
		return d, true
	case []byte:
		// SQLite-драйвер может вернуть числовую колонку как []byte (как и для
		// ключей группировки в normalizeGroupKey) — иначе значение молча
		// выпало бы из сумм/средних/сортировки.
		d, err := decimal.NewFromString(string(x))
		if err != nil {
			return decimal.Zero, false
		}
		return d, true
	}
	return decimal.Zero, false
}
