# Компоновка отчётов · Stage 1 (движок + рантайм + валидация) — Implementation Plan (план 59)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Отчёт с опциональным блоком `composition` (YAML) рендерится как сгруппированная таблица с промежуточными/общими итогами, условным оформлением и графиком; `onebase check` валидирует структуру компоновки. Старые отчёты не меняются.

**Architecture:** Чистый пакет `internal/report/compose` сворачивает плоские строки запроса (`[]map[string]any`) в дерево групп с итогами; условие `when` вычисляется через узкий интерфейс `Evaluator` (адаптер на интерпретаторе живёт в `ui`). Обработчик отчёта ветвится: `Composition==nil` → существующий плоский путь; иначе `Compose` → HTML-рендер + переиспользование слота `ChartOption`.

**Tech Stack:** Go; `shopspring/decimal` (точные деньги); `gopkg.in/yaml.v3`; `go-chi`; интерпретатор/парсер DSL (`internal/dsl/...`).

**Дизайн:** [59-report-composition-design.md](59-report-composition-design.md). **Ветка:** `feature/59-report-composition`.

> **Стейджинг v1.** Этот план — Stage 1: движок, рантайм-рендер, структурная валидация
> в `check`. Отдельными планами на той же ветке:
> **Stage 2** — визуальный конструктор в конфигураторе (вкладки Структура/Оформление/
> График/Предпросмотр, генерация YAML); **Stage 3** — экспорт составного отчёта в
> Excel/PDF + проверка существования полей композиции в колонках запроса (требует, чтобы
> `query.Compile` отдавал список выходных колонок). До Stage 2 отчёт авторится правкой
> YAML в уже существующем редакторе конфигуратора.

---

## Структура файлов

- Изменить: `internal/report/report.go` — типы `Composition`/`Measure`/`Totals`/`SortKey`/`CondRule`/`CellStyle`/`ChartSpec` + поле `Report.Composition`.
- Создать: `internal/report/report_composition_test.go` — тест парсинга YAML (с блоком и без).
- Создать: `internal/report/compose/compose.go` — `Compose`, типы `Result`/`Group`/`DetailRow`, `Evaluator`.
- Создать: `internal/report/compose/compose_test.go` — ядро юнит-тестов (фейковый `Evaluator`).
- Создать: `internal/ui/report_eval.go` — `interpEvaluator` (адаптер интерпретатора → `compose.Evaluator`).
- Создать: `internal/ui/report_eval_test.go` — тест адаптера на реальном интерпретаторе.
- Создать: `internal/ui/report_compose_render.go` — `renderComposedTable(*compose.Result, *report.Composition) template.HTML` + `buildComposedChart`.
- Создать: `internal/ui/report_compose_render_test.go` — структурный тест HTML.
- Изменить: `internal/ui/handlers_reports.go:118-142` — ветка композиции в `runReport`.
- Изменить: `internal/ui/templates.go` (шаблон `page-report`) — вывод `{{.ComposedHTML}}` при наличии.
- Изменить: `internal/configcheck/check.go` — `CheckReportComposition(proj)` + вызов в общем прогоне.
- Создать: `internal/configcheck/composition_check_test.go` — тесты структурной валидации.

---

## Task 1: Типы композиции в пакете `report`

**Files:**
- Modify: `internal/report/report.go`
- Test: `internal/report/report_composition_test.go`

- [ ] **Step 1: Написать падающий тест**

Создать `internal/report/report_composition_test.go`:

```go
package report

import "testing"

func TestParseComposition(t *testing.T) {
	src := []byte(`
name: Прод
query: "ВЫБРАТЬ 1"
composition:
  groupings: [Менеджер, Клиент]
  measures:
    - { field: Сумма, agg: sum, title: "Сумма, ₽" }
  totals: { grand: true, subtotals: true }
  detail: true
  sort: [ { field: Сумма, dir: desc } ]
  conditional:
    - { when: "Сумма < 0", field: "", style: { color: "#c00", bold: true } }
  chart: { type: bar, category: Менеджер, series: [Сумма] }
`)
	r, err := ParseBytes(src)
	if err != nil {
		t.Fatal(err)
	}
	if r.Composition == nil {
		t.Fatal("Composition is nil")
	}
	c := r.Composition
	if len(c.Groupings) != 2 || c.Groupings[0] != "Менеджер" {
		t.Fatalf("groupings: %v", c.Groupings)
	}
	if len(c.Measures) != 1 || c.Measures[0].Agg != "sum" || c.Measures[0].Title != "Сумма, ₽" {
		t.Fatalf("measures: %+v", c.Measures)
	}
	if !c.Totals.Grand || !c.Totals.Subtotals || !c.Detail {
		t.Fatalf("totals/detail: %+v %v", c.Totals, c.Detail)
	}
	if len(c.Conditional) != 1 || c.Conditional[0].Style.Color != "#c00" || !c.Conditional[0].Style.Bold {
		t.Fatalf("conditional: %+v", c.Conditional)
	}
	if c.Chart == nil || c.Chart.Type != "bar" || c.Chart.Category != "Менеджер" {
		t.Fatalf("chart: %+v", c.Chart)
	}
}

func TestParseNoComposition(t *testing.T) {
	r, err := ParseBytes([]byte("name: X\nquery: \"ВЫБРАТЬ 1\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Composition != nil {
		t.Fatal("Composition must be nil when absent")
	}
}
```

- [ ] **Step 2: Запустить тест — убедиться, что не компилируется/падает**

Run: `go test ./internal/report/ -run TestParseComposition -v`
Expected: FAIL (поле `Composition` не существует).

- [ ] **Step 3: Добавить типы и поле**

В `internal/report/report.go` в структуру `Report` добавить поле (рядом с `ChartProc`):

```go
	Composition *Composition `yaml:"composition"` // nil = плоская таблица (старое поведение)
```

И ниже определения `Report` добавить типы:

```go
type Composition struct {
	Groupings   []string   `yaml:"groupings"`
	Measures    []Measure  `yaml:"measures"`
	Totals      Totals     `yaml:"totals"`
	Detail      bool       `yaml:"detail"`
	Sort        []SortKey  `yaml:"sort"`
	Conditional []CondRule `yaml:"conditional"`
	Chart       *ChartSpec `yaml:"chart"`
}

type Measure struct {
	Field string `yaml:"field"`
	Agg   string `yaml:"agg"` // sum|count|avg|min|max ("" = sum)
	Title string `yaml:"title"`
}

type Totals struct {
	Grand     bool `yaml:"grand"`
	Subtotals bool `yaml:"subtotals"`
}

type SortKey struct {
	Field string `yaml:"field"`
	Dir   string `yaml:"dir"` // asc|desc
}

type CondRule struct {
	When  string    `yaml:"when"`
	Field string    `yaml:"field"` // "" = вся строка
	Style CellStyle `yaml:"style"`
}

type CellStyle struct {
	Color      string `yaml:"color"`
	Background string `yaml:"background"`
	Bold       bool   `yaml:"bold"`
	Italic     bool   `yaml:"italic"`
}

type ChartSpec struct {
	Type     string   `yaml:"type"` // bar|line|pie
	Category string   `yaml:"category"`
	Series   []string `yaml:"series"`
}
```

- [ ] **Step 4: Запустить тест — должен пройти**

Run: `go test ./internal/report/ -run 'TestParseComposition|TestParseNoComposition' -v`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/report/report.go internal/report/report_composition_test.go
git commit -m "feat(report): типы блока composition (план 59, stage 1)"
```

---

## Task 2: `compose` — одноуровневая группировка + sum + общий итог

**Files:**
- Create: `internal/report/compose/compose.go`
- Test: `internal/report/compose/compose_test.go`

- [ ] **Step 1: Написать падающий тест**

Создать `internal/report/compose/compose_test.go`:

```go
package compose

import (
	"testing"

	"github.com/ivantit66/onebase/internal/report"
)

// fakeEval — всегда совпадение по подстроке "<0" при отрицательной Сумме (для поздних тестов).
type noEval struct{}

func (noEval) EvalBool(string, Row) (bool, error) { return false, nil }

func decEq(t *testing.T, got any, want string) {
	t.Helper()
	d, ok := toDecimal(got)
	if !ok || d.String() != want {
		t.Fatalf("got %v (%T), want %s", got, got, want)
	}
}

func TestSingleGrouping(t *testing.T) {
	rows := []Row{
		{"Менеджер": "Иванов", "Сумма": "100"},
		{"Менеджер": "Иванов", "Сумма": "50"},
		{"Менеджер": "Петров", "Сумма": "30"},
	}
	spec := report.Composition{
		Groupings: []string{"Менеджер"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Totals:    report.Totals{Grand: true, Subtotals: true},
	}
	res, err := Compose(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 2 {
		t.Fatalf("groups=%d", len(res.Groups))
	}
	if res.Groups[0].Key != "Иванов" {
		t.Fatalf("order: %v", res.Groups[0].Key)
	}
	decEq(t, res.Groups[0].Subtotals["Сумма"], "150")
	decEq(t, res.Grand["Сумма"], "180")
	if res.RowCount != 3 || res.Capped {
		t.Fatalf("rowcount=%d capped=%v", res.RowCount, res.Capped)
	}
}
```

- [ ] **Step 2: Запустить — убедиться, что не компилируется**

Run: `go test ./internal/report/compose/ -run TestSingleGrouping -v`
Expected: FAIL (пакета/функций нет).

- [ ] **Step 3: Создать `compose.go` с минимальной реализацией**

Создать `internal/report/compose/compose.go`:

```go
// Package compose сворачивает плоские строки отчёта в дерево групп с итогами.
// Чистый слой: без БД/HTTP. Условия оформления вычисляются через Evaluator.
package compose

import (
	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/report"
)

type Row = map[string]any

// Evaluator вычисляет булево DSL-выражение при значениях полей строки.
type Evaluator interface {
	EvalBool(expr string, row Row) (bool, error)
}

const DefaultMaxRows = 50000

type Result struct {
	Columns  []string
	Groups   []*Group
	Grand    map[string]any
	RowCount int
	Capped   bool
}

type Group struct {
	Field     string
	Key       any
	Subtotals map[string]any
	Count     int
	Children  []*Group
	Details   []DetailRow
}

type DetailRow struct {
	Values map[string]any
	Styles map[string]report.CellStyle // ключ "" = стиль на всю строку
}

func Compose(rows []Row, spec report.Composition, ev Evaluator) (*Result, error) {
	return ComposeN(rows, spec, ev, DefaultMaxRows)
}

func ComposeN(rows []Row, spec report.Composition, ev Evaluator, maxRows int) (*Result, error) {
	res := &Result{RowCount: len(rows)}
	if maxRows > 0 && len(rows) > maxRows {
		rows = rows[:maxRows]
		res.Capped = true
		res.RowCount = maxRows
	}
	res.Groups = buildGroups(rows, spec, 0, ev)
	res.Grand = aggregate(rows, spec.Measures)
	return res, nil
}

func buildGroups(rows []Row, spec report.Composition, level int, ev Evaluator) []*Group {
	if level >= len(spec.Groupings) {
		return nil
	}
	field := spec.Groupings[level]
	var order []any
	buckets := map[any][]Row{}
	for _, r := range rows {
		k := r[field]
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
			Subtotals: aggregate(buckets[k], spec.Measures),
		}
		groups = append(groups, gr)
	}
	return groups
}

func aggregate(rows []Row, measures []report.Measure) map[string]any {
	out := map[string]any{}
	for _, m := range measures {
		out[m.Field] = aggMeasure(rows, m)
	}
	return out
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

func toDecimal(v any) (decimal.Decimal, bool) {
	switch x := v.(type) {
	case decimal.Decimal:
		return x, true
	case int:
		return decimal.NewFromInt(int64(x)), true
	case int64:
		return decimal.NewFromInt(x), true
	case float64:
		return decimal.NewFromFloat(x), true
	case string:
		d, err := decimal.NewFromString(x)
		if err != nil {
			return decimal.Zero, false
		}
		return d, true
	}
	return decimal.Zero, false
}
```

- [ ] **Step 4: Запустить — должен пройти**

Run: `go test ./internal/report/compose/ -run TestSingleGrouping -v`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/report/compose/
git commit -m "feat(compose): одноуровневая группировка, sum, общий итог (план 59)"
```

---

## Task 3: `compose` — многоуровневая вложенность + детали

**Files:**
- Modify: `internal/report/compose/compose.go`
- Test: `internal/report/compose/compose_test.go`

- [ ] **Step 1: Добавить падающий тест**

В `compose_test.go` добавить:

```go
func TestNestedAndDetails(t *testing.T) {
	rows := []Row{
		{"М": "И", "К": "Р", "Сумма": "600"},
		{"М": "И", "К": "Р", "Сумма": "380"},
		{"М": "И", "К": "П", "Сумма": "270"},
	}
	spec := report.Composition{
		Groupings: []string{"М", "К"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Detail:    true,
	}
	res, err := Compose(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 1 || len(res.Groups[0].Children) != 2 {
		t.Fatalf("tree: %+v", res.Groups)
	}
	rom := res.Groups[0].Children[0]
	decEq(t, rom.Subtotals["Сумма"], "980")
	if len(rom.Details) != 2 {
		t.Fatalf("details=%d", len(rom.Details))
	}
	if rom.Details[0].Values["Сумма"] != "600" {
		t.Fatalf("detail val: %v", rom.Details[0].Values["Сумма"])
	}
}
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./internal/report/compose/ -run TestNestedAndDetails -v`
Expected: FAIL (нет вложенности/деталей — `Children`/`Details` пусты).

- [ ] **Step 3: Дополнить `buildGroups`**

В `compose.go` заменить тело цикла по `order` в `buildGroups` (добавить рекурсию и детали) — внутри `for _, k := range order` после создания `gr`:

```go
		if level+1 < len(spec.Groupings) {
			gr.Children = buildGroups(buckets[k], spec, level+1, ev)
		} else if spec.Detail {
			gr.Details = buildDetails(buckets[k], spec, ev)
		}
```

И добавить функцию:

```go
func buildDetails(rows []Row, spec report.Composition, ev Evaluator) []DetailRow {
	out := make([]DetailRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, DetailRow{Values: r})
	}
	return out
}
```

- [ ] **Step 4: Запустить — должен пройти**

Run: `go test ./internal/report/compose/ -run 'TestNested|TestSingle' -v`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/report/compose/
git commit -m "feat(compose): вложенные группировки и детальные строки (план 59)"
```

---

## Task 4: `compose` — count/avg/min/max + decimal-деньги

**Files:**
- Modify: `internal/report/compose/compose_test.go` (только тест — реализация уже в `aggMeasure`)

- [ ] **Step 1: Добавить тест**

```go
func TestAggregates(t *testing.T) {
	rows := []Row{
		{"Г": "A", "X": "10.50"},
		{"Г": "A", "X": "4.50"},
	}
	mk := func(agg string) any {
		spec := report.Composition{Groupings: []string{"Г"}, Measures: []report.Measure{{Field: "X", Agg: agg}}}
		res, _ := Compose(rows, spec, noEval{})
		return res.Groups[0].Subtotals["X"]
	}
	decEq(t, mk("sum"), "15")
	decEq(t, mk("avg"), "7.5")
	decEq(t, mk("min"), "4.5")
	decEq(t, mk("max"), "10.5")
	if c, _ := mk("count").(int64); c != 2 {
		t.Fatalf("count=%v", mk("count"))
	}
}
```

- [ ] **Step 2: Запустить — должен пройти сразу** (реализация в `aggMeasure` уже покрывает)

Run: `go test ./internal/report/compose/ -run TestAggregates -v`
Expected: PASS. Если FAIL — исправить `aggMeasure` под показанное поведение.

- [ ] **Step 3: Коммит**

```bash
git add internal/report/compose/
git commit -m "test(compose): агрегаты count/avg/min/max на decimal (план 59)"
```

---

## Task 5: `compose` — сортировка на каждом уровне

**Files:**
- Modify: `internal/report/compose/compose.go`
- Test: `internal/report/compose/compose_test.go`

- [ ] **Step 1: Добавить падающий тест**

```go
func TestSort(t *testing.T) {
	rows := []Row{
		{"М": "А", "Сумма": "100"},
		{"М": "Б", "Сумма": "300"},
		{"М": "В", "Сумма": "200"},
	}
	spec := report.Composition{
		Groupings: []string{"М"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Sort:      []report.SortKey{{Field: "Сумма", Dir: "desc"}},
	}
	res, _ := Compose(rows, spec, noEval{})
	got := []any{res.Groups[0].Key, res.Groups[1].Key, res.Groups[2].Key}
	if got[0] != "Б" || got[1] != "В" || got[2] != "А" {
		t.Fatalf("order by subtotal desc: %v", got)
	}
}
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./internal/report/compose/ -run TestSort -v`
Expected: FAIL (группы в порядке появления, не по сумме).

- [ ] **Step 3: Реализовать сортировку**

В `compose.go` добавить `import "sort"` и в конце `buildGroups` перед `return groups` вставить:

```go
	sortGroups(groups, spec)
```

В `buildDetails` перед `return out` вставить:

```go
	sortDetails(out, spec)
```

Добавить функции:

```go
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
func compareVals(a, b any) int {
	da, oka := toDecimal(a)
	db, okb := toDecimal(b)
	if oka && okb {
		return da.Cmp(db)
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
	return decimalOrSprint(v)
}

func decimalOrSprint(v any) string {
	if d, ok := toDecimal(v); ok {
		return d.String()
	}
	return ""
}
```

- [ ] **Step 4: Запустить — должен пройти**

Run: `go test ./internal/report/compose/ -v`
Expected: PASS (все тесты compose).

- [ ] **Step 5: Коммит**

```bash
git add internal/report/compose/
git commit -m "feat(compose): сортировка групп и деталей по уровням (план 59)"
```

---

## Task 6: `compose` — условное оформление + потолок строк

**Files:**
- Modify: `internal/report/compose/compose.go`
- Test: `internal/report/compose/compose_test.go`

- [ ] **Step 1: Добавить падающий тест**

```go
// negEval: совпадение, когда выражение содержит "<0" и Сумма отрицательна.
type negEval struct{}

func (negEval) EvalBool(expr string, row Row) (bool, error) {
	d, ok := toDecimal(row["Сумма"])
	return ok && d.Sign() < 0, nil
}

func TestConditionalAndCap(t *testing.T) {
	rows := []Row{
		{"М": "A", "Сумма": "-45"},
		{"М": "A", "Сумма": "10"},
	}
	spec := report.Composition{
		Groupings: []string{"М"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Detail:    true,
		Conditional: []report.CondRule{
			{When: "Сумма < 0", Field: "", Style: report.CellStyle{Color: "#c00", Bold: true}},
		},
	}
	res, _ := Compose(rows, spec, negEval{})
	d := res.Groups[0].Details
	if d[0].Styles[""].Color != "#c00" || !d[0].Styles[""].Bold {
		t.Fatalf("styles[0]: %+v", d[0].Styles)
	}
	if _, ok := d[1].Styles[""]; ok {
		t.Fatalf("row 1 must be unstyled: %+v", d[1].Styles)
	}

	// потолок строк
	res2, _ := ComposeN(rows, spec, negEval{}, 1)
	if !res2.Capped || res2.RowCount != 1 {
		t.Fatalf("cap: capped=%v rowcount=%d", res2.Capped, res2.RowCount)
	}
}
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./internal/report/compose/ -run TestConditionalAndCap -v`
Expected: FAIL (стили не проставляются).

- [ ] **Step 3: Реализовать стили в `buildDetails`**

В `compose.go` заменить тело `buildDetails` (до `sortDetails`):

```go
func buildDetails(rows []Row, spec report.Composition, ev Evaluator) []DetailRow {
	out := make([]DetailRow, 0, len(rows))
	for _, r := range rows {
		dr := DetailRow{Values: r}
		if len(spec.Conditional) > 0 && ev != nil {
			styles := map[string]report.CellStyle{}
			for _, rule := range spec.Conditional {
				if _, done := styles[rule.Field]; done {
					continue // первое сработавшее правило на целевое поле
				}
				ok, err := ev.EvalBool(rule.When, r)
				if err != nil || !ok {
					continue
				}
				styles[rule.Field] = rule.Style
			}
			if len(styles) > 0 {
				dr.Styles = styles
			}
		}
		out = append(out, dr)
	}
	sortDetails(out, spec)
	return out
}
```

- [ ] **Step 4: Запустить — весь пакет зелёный**

Run: `go test ./internal/report/compose/ -v`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/report/compose/
git commit -m "feat(compose): условное оформление и потолок строк (план 59)"
```

---

## Task 7: Адаптер `interpEvaluator` (DSL `when` → bool)

**Files:**
- Create: `internal/ui/report_eval.go`
- Test: `internal/ui/report_eval_test.go`

Адаптер компилирует выражение в синтетическую процедуру `Функция __cond() Возврат (<when>); КонецФункции` один раз и исполняет её на каждую строку через `RunWithResult` с полями строки как `extraVars` — тот же паттерн, что `runChartProc` (`handlers_reports.go:145`).

- [ ] **Step 1: Написать падающий тест**

Создать `internal/ui/report_eval_test.go`:

```go
package ui

import (
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/report/compose"
)

func TestInterpEvaluator(t *testing.T) {
	ev := newInterpEvaluator(interpreter.New())
	ok, err := ev.EvalBool("Сумма < 0", compose.Row{"Сумма": "-45"})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("ожидали true для Сумма<0")
	}
	ok, _ = ev.EvalBool("Сумма < 0", compose.Row{"Сумма": "10"})
	if ok {
		t.Fatal("ожидали false для Сумма>=0")
	}
}
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./internal/ui/ -run TestInterpEvaluator -v`
Expected: FAIL (нет `newInterpEvaluator`).

- [ ] **Step 3: Реализовать адаптер**

Создать `internal/ui/report_eval.go`:

```go
package ui

import (
	"fmt"
	"sync"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/report/compose"
)

// interpEvaluator вычисляет DSL-условия `when` на интерпретаторе.
// Каждое выражение компилируется в процедуру один раз (кэш), исполняется
// на строку через RunWithResult с полями строки как переменными.
type interpEvaluator struct {
	interp *interpreter.Interpreter
	mu     sync.Mutex
	cache  map[string]*ast.ProcedureDecl
}

func newInterpEvaluator(interp *interpreter.Interpreter) *interpEvaluator {
	return &interpEvaluator{interp: interp, cache: map[string]*ast.ProcedureDecl{}}
}

func (e *interpEvaluator) compile(expr string) (*ast.ProcedureDecl, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p, ok := e.cache[expr]; ok {
		return p, nil
	}
	src := "Функция __cond()\nВозврат (" + expr + ");\nКонецФункции\n"
	prog, err := parser.New(lexer.New(src, "cond.os")).ParseProgram()
	if err != nil {
		return nil, err
	}
	var proc *ast.ProcedureDecl
	for _, d := range prog.Procedures {
		proc = d
		break
	}
	if proc == nil {
		return nil, fmt.Errorf("пустое выражение")
	}
	e.cache[expr] = proc
	return proc, nil
}

func (e *interpEvaluator) EvalBool(expr string, row compose.Row) (bool, error) {
	proc, err := e.compile(expr)
	if err != nil {
		return false, err
	}
	var result any
	if err := e.interp.RunWithResult(proc, &interpreter.MapThis{M: row}, &result, map[string]any(row)); err != nil {
		return false, err
	}
	b, _ := result.(bool)
	return b, nil
}
```

> Подтверждено: `ast.Program.Procedures []*ast.ProcedureDecl` (`internal/dsl/ast/ast.go:16`);
> функции `Функция` тоже попадают сюда. Берём первый элемент.

- [ ] **Step 4: Запустить — должен пройти**

Run: `go test ./internal/ui/ -run TestInterpEvaluator -v`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/ui/report_eval.go internal/ui/report_eval_test.go
git commit -m "feat(ui): interpEvaluator — условия when на интерпретаторе (план 59)"
```

---

## Task 8: HTML-рендер составной таблицы

**Files:**
- Create: `internal/ui/report_compose_render.go`
- Test: `internal/ui/report_compose_render_test.go`

- [ ] **Step 1: Написать падающий тест**

Создать `internal/ui/report_compose_render_test.go`:

```go
package ui

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
)

func TestRenderComposedTable(t *testing.T) {
	rows := []compose.Row{
		{"М": "Иванов", "Сумма": "150"},
		{"М": "Петров", "Сумма": "30"},
	}
	spec := report.Composition{
		Groupings: []string{"М"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum", Title: "Сумма, ₽"}},
		Totals:    report.Totals{Grand: true, Subtotals: true},
	}
	res, _ := compose.Compose(rows, spec, nil)
	html := string(renderComposedTable(res, &spec))
	for _, want := range []string{"Иванов", "Петров", "150", "Сумма, ₽", "ВСЕГО", "data-group", "<table"} {
		if !strings.Contains(html, want) {
			t.Fatalf("HTML не содержит %q:\n%s", want, html)
		}
	}
}
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./internal/ui/ -run TestRenderComposedTable -v`
Expected: FAIL (нет `renderComposedTable`).

- [ ] **Step 3: Реализовать рендер**

Создать `internal/ui/report_compose_render.go`:

```go
package ui

import (
	"fmt"
	"html"
	"html/template"
	"strings"

	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
)

// renderComposedTable строит единую <table> с раскрываемыми группами,
// подитогами, общим итогом и условным оформлением деталей.
func renderComposedTable(res *compose.Result, spec *report.Composition) template.HTML {
	var b strings.Builder
	b.WriteString(`<table class="report-composed">`)
	// шапка
	b.WriteString(`<thead><tr><th>` + html.EscapeString(strings.Join(spec.Groupings, " / ")) + `</th>`)
	for _, m := range spec.Measures {
		b.WriteString(`<th class="num">` + html.EscapeString(measureTitle(m)) + `</th>`)
	}
	b.WriteString(`</tr></thead><tbody>`)
	for _, g := range res.Groups {
		writeGroup(&b, g, spec, 0, "")
	}
	if spec.Totals.Grand {
		b.WriteString(`<tr class="grand"><td>ВСЕГО</td>`)
		for _, m := range spec.Measures {
			b.WriteString(`<td class="num">` + html.EscapeString(fmtVal(res.Grand[m.Field])) + `</td>`)
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)
	return template.HTML(b.String())
}

func writeGroup(b *strings.Builder, g *compose.Group, spec *report.Composition, level int, path string) {
	gp := path + "/" + fmtVal(g.Key)
	pad := fmt.Sprintf("padding-left:%dpx", 8+level*18)
	fmt.Fprintf(b, `<tr class="grp" data-group="%s" data-level="%d"><td style="%s">▼ %s</td>`,
		html.EscapeString(gp), level, pad, html.EscapeString(fmtVal(g.Key)))
	for _, m := range spec.Measures {
		b.WriteString(`<td class="num">` + html.EscapeString(fmtVal(g.Subtotals[m.Field])) + `</td>`)
	}
	b.WriteString(`</tr>`)
	for _, ch := range g.Children {
		writeGroup(b, ch, spec, level+1, gp)
	}
	for _, d := range g.Details {
		writeDetail(b, d, spec, level+1, gp)
	}
	if spec.Totals.Subtotals {
		fmt.Fprintf(b, `<tr class="subtotal" data-parent="%s"><td style="padding-left:%dpx">··· Итого: %s ···</td>`,
			html.EscapeString(gp), 8+(level+1)*18, html.EscapeString(fmtVal(g.Key)))
		for _, m := range spec.Measures {
			b.WriteString(`<td class="num">` + html.EscapeString(fmtVal(g.Subtotals[m.Field])) + `</td>`)
		}
		b.WriteString(`</tr>`)
	}
}

func writeDetail(b *strings.Builder, d compose.DetailRow, spec *report.Composition, level int, path string) {
	rowStyle := cssOf(d.Styles[""])
	fmt.Fprintf(b, `<tr class="det" data-parent="%s" style="%s">`, html.EscapeString(path), rowStyle)
	// первая колонка — пусто (отступ деталей); затем меры со значениями строки
	fmt.Fprintf(b, `<td style="padding-left:%dpx"></td>`, 8+level*18)
	for _, m := range spec.Measures {
		cell := cssOf(d.Styles[m.Field])
		b.WriteString(`<td class="num" style="` + cell + `">` + html.EscapeString(fmtVal(d.Values[m.Field])) + `</td>`)
	}
	b.WriteString(`</tr>`)
}

func cssOf(s report.CellStyle) string {
	var p []string
	if s.Color != "" {
		p = append(p, "color:"+s.Color)
	}
	if s.Background != "" {
		p = append(p, "background:"+s.Background)
	}
	if s.Bold {
		p = append(p, "font-weight:bold")
	}
	if s.Italic {
		p = append(p, "font-style:italic")
	}
	return strings.Join(p, ";")
}

func measureTitle(m report.Measure) string {
	if m.Title != "" {
		return m.Title
	}
	return m.Field
}

func fmtVal(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
```

> JS-сворачивание по `data-group`/`data-parent` добавляется в шаблон `page-report`
> (Task 10). Рендер отдаёт всё дерево; видимость переключает клиент.

- [ ] **Step 4: Запустить — должен пройти**

Run: `go test ./internal/ui/ -run TestRenderComposedTable -v`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/ui/report_compose_render.go internal/ui/report_compose_render_test.go
git commit -m "feat(ui): HTML-рендер составной таблицы отчёта (план 59)"
```

---

## Task 9: Декларативный график из `Result`

**Files:**
- Modify: `internal/ui/report_compose_render.go`
- Test: `internal/ui/report_compose_render_test.go`

- [ ] **Step 1: Добавить падающий тест**

```go
func TestBuildComposedChart(t *testing.T) {
	rows := []compose.Row{{"М": "Иванов", "Сумма": "150"}, {"М": "Петров", "Сумма": "30"}}
	spec := report.Composition{
		Groupings: []string{"М"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Chart:     &report.ChartSpec{Type: "bar", Category: "М", Series: []string{"Сумма"}},
	}
	res, _ := compose.Compose(rows, spec, nil)
	opt := buildComposedChart(res, spec.Chart)
	if opt == nil {
		t.Fatal("nil chart option")
	}
	xAxis, _ := opt["xAxis"].(map[string]any)
	cats, _ := xAxis["data"].([]string)
	if len(cats) != 2 || cats[0] != "Иванов" {
		t.Fatalf("categories: %v", cats)
	}
}
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./internal/ui/ -run TestBuildComposedChart -v`
Expected: FAIL.

- [ ] **Step 3: Реализовать**

В `report_compose_render.go` добавить:

```go
// buildComposedChart строит ECharts-option из верхнего уровня группировки.
// Формат совпадает с тем, что отдаёт ChartProc (слот ChartOption шаблона).
func buildComposedChart(res *compose.Result, c *report.ChartSpec) map[string]any {
	if c == nil || len(res.Groups) == 0 {
		return nil
	}
	cats := make([]string, 0, len(res.Groups))
	for _, g := range res.Groups {
		cats = append(cats, fmtVal(g.Key))
	}
	var series []any
	for _, sf := range c.Series {
		data := make([]any, 0, len(res.Groups))
		for _, g := range res.Groups {
			data = append(data, numFor(g.Subtotals[sf]))
		}
		s := map[string]any{"name": sf, "type": chartType(c.Type), "data": data}
		series = append(series, s)
	}
	opt := map[string]any{
		"tooltip": map[string]any{},
		"series":  series,
	}
	if c.Type == "pie" {
		// для круговой — один ряд из пар {name,value}
		pie := make([]any, 0, len(res.Groups))
		for _, g := range res.Groups {
			pie = append(pie, map[string]any{"name": fmtVal(g.Key), "value": numFor(g.Subtotals[firstSeries(c)])})
		}
		opt["series"] = []any{map[string]any{"type": "pie", "data": pie}}
		return opt
	}
	opt["xAxis"] = map[string]any{"type": "category", "data": cats}
	opt["yAxis"] = map[string]any{"type": "value"}
	return opt
}

func chartType(t string) string {
	if t == "line" {
		return "line"
	}
	return "bar"
}

func firstSeries(c *report.ChartSpec) string {
	if len(c.Series) > 0 {
		return c.Series[0]
	}
	return ""
}

func numFor(v any) any {
	if d, ok := compose.ExportToDecimal(v); ok {
		f, _ := d.Float64()
		return f
	}
	return 0
}
```

В `internal/report/compose/compose.go` экспортировать хелпер (добавить):

```go
// ExportToDecimal — toDecimal для внешних пакетов (ui-рендер графика).
func ExportToDecimal(v any) (decimal.Decimal, bool) { return toDecimal(v) }
```

- [ ] **Step 4: Запустить — должен пройти**

Run: `go test ./internal/ui/ -run TestBuildComposedChart -v && go test ./internal/report/compose/ -v`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/ui/report_compose_render.go internal/ui/report_compose_render_test.go internal/report/compose/compose.go
git commit -m "feat(ui): декларативный график составного отчёта (план 59)"
```

---

## Task 10: Ветка композиции в обработчике + шаблон

**Files:**
- Modify: `internal/ui/handlers_reports.go:128-142`
- Modify: `internal/ui/templates.go` (шаблон `page-report`)

- [ ] **Step 1: Подключить компоновку в `runReport`**

В `internal/ui/handlers_reports.go` после `s.resolveUUIDsInReport(r.Context(), rows)` (строка 128) и до блока `var chartOption ...` вставить ветку: если `rep.Composition != nil` — собрать составной отчёт и отрендерить, иначе оставить существующий путь без изменений.

Заменить блок строк 130-142 на:

```go
	if rep.Composition != nil {
		ev := newInterpEvaluator(s.interp)
		res, cerr := compose.Compose(rows, *rep.Composition, ev)
		if cerr != nil {
			s.render(w, r, "page-report", map[string]any{
				"Report": rep, "QueryError": cerr.Error(),
				"ParamValues": paramValues, "ReportParams": reportParams,
			})
			return
		}
		var chartOption map[string]any
		if rep.Composition.Chart != nil {
			chartOption = buildComposedChart(res, rep.Composition.Chart)
		}
		s.render(w, r, "page-report", map[string]any{
			"Report":       rep,
			"ComposedHTML": renderComposedTable(res, rep.Composition),
			"Capped":       res.Capped,
			"ChartOption":  chartOption,
			"ParamValues":  paramValues,
			"ReportParams": reportParams,
		})
		return
	}

	var chartOption map[string]any
	if rep.ChartProc != "" {
		chartOption = s.runChartProc(r.Context(), rep, rows, paramValues)
	}

	s.render(w, r, "page-report", map[string]any{
		"Report":       rep,
		"Cols":         cols,
		"Rows":         rows,
		"ParamValues":  paramValues,
		"ChartOption":  chartOption,
		"ReportParams": reportParams,
	})
```

Добавить импорт `"github.com/ivantit66/onebase/internal/report/compose"` в блок импортов (рядом с `reportpkg`).

- [ ] **Step 2: Шаблон — вывести ComposedHTML + JS-сворачивание**

В `internal/ui/templates.go` найти шаблон `page-report` (блок с таблицей `Cols`/`Rows`).
Перед существующим выводом таблицы добавить ветку:

```html
{{if .Capped}}<div class="warn">Показаны первые строки — данных больше потолка.</div>{{end}}
{{if .ComposedHTML}}
  {{.ComposedHTML}}
  <script>
  (function(){
    document.querySelectorAll('tr.grp').forEach(function(tr){
      tr.style.cursor='pointer';
      tr.addEventListener('click', function(){
        var key=tr.getAttribute('data-group');
        var cell=tr.querySelector('td'); var open=cell.textContent.indexOf('▼')===0;
        document.querySelectorAll('[data-parent^="'+key+'"]').forEach(function(el){
          el.style.display = open ? 'none' : '';
        });
        if(cell) cell.textContent = (open?'▶':'▼')+cell.textContent.slice(1);
      });
    });
  })();
  </script>
{{else}}
  <!-- существующая плоская таблица Cols/Rows без изменений -->
```

И закрыть существующую таблицу соответствующим `{{end}}` (обернуть текущий блок таблицы в `{{else}} ... {{end}}`). Точную обёртку определить по месту: ветвление `{{if .ComposedHTML}}…{{else}}<текущая таблица>{{end}}`.

- [ ] **Step 3: Собрать и прогнать тесты пакета**

Run: `go build ./... && go test ./internal/ui/ -run 'Report|Compose|Eval' -v`
Expected: компиляция без ошибок; тесты PASS.

- [ ] **Step 4: Ручная проверка (smoke)**

Создать в тест-конфиге отчёт с блоком `composition` (см. дизайн), запустить
`onebase run --project <dir> --sqlite test.db --port 8080`, открыть отчёт →
видны группы, подитоги, ВСЕГО, раскрытие/сворачивание по клику, график.

- [ ] **Step 5: Коммит**

```bash
git add internal/ui/handlers_reports.go internal/ui/templates.go
git commit -m "feat(ui): рендер составного отчёта в runReport + шаблон (план 59)"
```

---

## Task 11: Структурная валидация композиции в `onebase check`

**Files:**
- Modify: `internal/configcheck/check.go`
- Test: `internal/configcheck/composition_check_test.go`

Проверяет внутреннюю согласованность (без выполнения запроса): `agg ∈ {sum,count,avg,min,max,""}`, `dir ∈ {asc,desc,""}`, `chart.type ∈ {bar,line,pie}`, `chart.category ∈ groupings`, `chart.series ⊆ measure-поля`, `sort.field ∈ groupings∪measures`, каждое `when` парсится.

> Проверка существования полей группировок/мер в **колонках запроса** — Stage 3
> (нужен список выходных колонок из `query.Compile`).

- [ ] **Step 1: Написать падающие тесты**

Создать `internal/configcheck/composition_check_test.go`:

```go
package configcheck

import (
	"testing"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/report"
)

func projWith(c *report.Composition) *project.Project {
	return &project.Project{Reports: []*report.Report{{Name: "R", Query: "ВЫБРАТЬ 1", Composition: c}}}
}

func TestCompositionOK(t *testing.T) {
	c := &report.Composition{
		Groupings: []string{"М"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Sort:      []report.SortKey{{Field: "Сумма", Dir: "desc"}},
		Chart:     &report.ChartSpec{Type: "bar", Category: "М", Series: []string{"Сумма"}},
		Conditional: []report.CondRule{{When: "Сумма < 0"}},
	}
	if iss := CheckReportComposition(projWith(c)); len(iss) != 0 {
		t.Fatalf("ожидали 0 проблем: %+v", iss)
	}
}

func TestCompositionBad(t *testing.T) {
	c := &report.Composition{
		Groupings:   []string{"М"},
		Measures:    []report.Measure{{Field: "Сумма", Agg: "wat"}},
		Chart:       &report.ChartSpec{Type: "donut", Category: "Нет", Series: []string{"Икс"}},
		Conditional: []report.CondRule{{When: "Сумма < "}}, // битое выражение
	}
	iss := CheckReportComposition(projWith(c))
	if len(iss) < 3 {
		t.Fatalf("ожидали несколько проблем, получили %d: %+v", len(iss), iss)
	}
}
```

> Подтверждено: `project.Project.Reports []*report.Report` (`internal/project/loader.go:35`).

- [ ] **Step 2: Запустить — падает**

Run: `go test ./internal/configcheck/ -run TestComposition -v`
Expected: FAIL (нет `CheckReportComposition`).

- [ ] **Step 3: Реализовать валидатор**

В `internal/configcheck/check.go` добавить (импорты `lexer`/`parser` уже используются в `ParseDSL`):

```go
// CheckReportComposition валидирует структуру блока composition отчётов
// (без выполнения запроса). Проверка полей против колонок запроса — Stage 3.
func CheckReportComposition(proj *project.Project) []Issue {
	var issues []Issue
	aggs := map[string]bool{"": true, "sum": true, "count": true, "avg": true, "min": true, "max": true}
	dirs := map[string]bool{"": true, "asc": true, "desc": true}
	ctypes := map[string]bool{"bar": true, "line": true, "pie": true}
	add := func(name, msg string) {
		issues = append(issues, Issue{File: "reports/" + name + ".yaml", Object: name, Kind: "Отчёт (компоновка)", Message: msg})
	}
	for _, rep := range proj.Reports {
		c := rep.Composition
		if c == nil {
			continue
		}
		groups := map[string]bool{}
		for _, g := range c.Groupings {
			groups[g] = true
		}
		measures := map[string]bool{}
		for _, m := range c.Measures {
			measures[m.Field] = true
			if !aggs[m.Agg] {
				add(rep.Name, "неизвестный агрегат: "+m.Agg)
			}
		}
		for _, s := range c.Sort {
			if !dirs[s.Dir] {
				add(rep.Name, "неизвестное направление сортировки: "+s.Dir)
			}
			if !groups[s.Field] && !measures[s.Field] {
				add(rep.Name, "поле сортировки не группировка и не показатель: "+s.Field)
			}
		}
		if c.Chart != nil {
			if !ctypes[c.Chart.Type] {
				add(rep.Name, "неизвестный тип графика: "+c.Chart.Type)
			}
			if !groups[c.Chart.Category] {
				add(rep.Name, "категория графика не входит в группировки: "+c.Chart.Category)
			}
			for _, sname := range c.Chart.Series {
				if !measures[sname] {
					add(rep.Name, "ряд графика не входит в показатели: "+sname)
				}
			}
		}
		for _, cr := range c.Conditional {
			src := "Функция __cond()\nВозврат (" + cr.When + ");\nКонецФункции\n"
			if _, err := parser.New(lexer.New(src, "cond.os")).ParseProgram(); err != nil {
				add(rep.Name, "ошибка выражения условия \""+cr.When+"\": "+err.Error())
			}
		}
	}
	return issues
}
```

Подключить в основной прогон проверок: найти, где собираются `CheckQueries(...)`
(возле `check.go:252`/общая функция, агрегирующая `[]Issue`), и добавить
`issues = append(issues, CheckReportComposition(proj)...)`.

- [ ] **Step 4: Запустить — должен пройти**

Run: `go test ./internal/configcheck/ -run TestComposition -v`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/configcheck/check.go internal/configcheck/composition_check_test.go
git commit -m "feat(check): структурная валидация компоновки отчётов (план 59)"
```

---

## Task 12: Зелёная сборка целиком + регрессия

**Files:** —

- [ ] **Step 1: Полный прогон**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: всё зелёное (старые тесты отчётов не сломаны — путь `Composition==nil` неизменен).

- [ ] **Step 2: Проверка обратной совместимости**

Прогнать существующий отчёт без `composition` через `onebase check --project <dir>` и
открыть его в `onebase run` — поведение прежнее (плоская таблица, ChartProc работает).

- [ ] **Step 3: Коммит (если были правки)**

```bash
git add -A
git commit -m "chore(report): зелёная сборка stage 1 компоновки отчётов (план 59)"
```

---

## Self-review (выполнено при написании)

- **Покрытие дизайна (Stage 1):** типы (Task 1), движок группировок/итогов/сортировки/
  оформления/потолка (Task 2–6), эвалюатор `when` (Task 7), рендер таблицы + график
  (Task 8–9), ветка обработчика + шаблон со сворачиванием (Task 10), валидация `check`
  (Task 11), регрессия (Task 12). Экспорт и конструктор — Stage 2/3 (заявлено в шапке).
- **Типы согласованы:** `compose.Row`, `compose.Result/Group/DetailRow`, `report.Composition`
  и под-типы, `interpEvaluator`, `renderComposedTable`/`buildComposedChart`,
  `compose.ExportToDecimal` — имена совпадают между задачами.
- **Точки, требующие сверки при исполнении (помечены `>`):** имя поля списка процедур в
  `ast.Program` (Task 7, подтверждено `Procedures`), фактические типы
  `project.Project.Reports` (Task 11, подтверждено `[]*report.Report`). В шаблоне
  `page-report` использован отдельный блок `{{if .ComposedHTML}}` (не `{{else}}` к
  `{{if .Cols}}`): composed-отчёт не задаёт `Cols`, поэтому блоки взаимоисключаются
  естественно, флэт-таблица не тронута.

## Известные ограничения Stage 1 → follow-ups для Stage 2/3 (по код-ревью)

Зафиксировано при subagent-ревью; не ship-blocker для Stage 1, но закрыть позже:

- **Экспорт составного отчёта (Stage 3).** Кнопка Excel в composed-блоке убрана: текущий
  `/excel` отдаёт плоские строки запроса без группировок/итогов. Stage 3 должен гонять
  `compose.Compose` и выгружать дерево (группы/подитоги/итог) в Excel/PDF, и вернуть кнопку.
- **i18n подписей таблицы (Stage 2/3).** `renderComposedTable` хардкодит «ВСЕГО» и
  «··· Итого: X ···» по-русски. Протянуть `lang` и переводить через `globalBundle.T`
  (с nil-guard для юнит-тестов рендера), добавить ключи в локали.
- **`interpEvaluator` на запрос (perf).** Сейчас создаётся в `runReport` на каждый запрос →
  кэш скомпилированных `when` живёт один запрос, `sync.Mutex` фактически не нужен.
  Перенести кэш на уровень `Server`/реестра (один на процесс), т.к. `when` из статичного YAML.
- **Молчаливый не-bool / ошибка `EvalBool`.** `compose.Compose` всегда возвращает `nil` error
  (ветка `cerr != nil` в обработчике — оборонительная), а ошибки `when` глотаются в
  `buildDetails`. Достаточно для v1 (синтаксис ловит `onebase check`), но при наличии
  логгера стоит писать предупреждение о неверном `when`.
- **Вложенное сворачивание (UX).** Префиксное `display:none` не сохраняет индивидуальное
  состояние вложенных групп: свернуть ребёнка → свернуть родителя → развернуть родителя
  показывает заголовок ребёнка, но его детали остаются скрытыми. Для устранения — хранить
  состояние свёрнутости per-group, а не чистый prefix-toggle.
- **Формат чисел в составной таблице (cosmetic).** `fmtVal` выводит меры через
  `fmt.Sprintf("%v")` — без разрядки тысяч (плоский отчёт использует `fmtReportCell` →
  `1 250 000`), а `avg` может дать 16 знаков. Нужен decimal-aware форматтер для ячеек-мер
  (разрядка + округление), не затрагивая подписи группировок. Значения корректны, расходится
  только отображение.
