package launcher

import (
	"net/url"
	"strings"
	"testing"
)

func TestParseCompositionForm(t *testing.T) {
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.grouping.0", "Менеджер")
	f.Set("comp.grouping.1", "Клиент")
	f.Set("comp.measure.0.field", "Сумма")
	f.Set("comp.measure.0.agg", "sum")
	f.Set("comp.measure.0.title", "Сумма, ₽")
	f.Set("comp.totals.grand", "on")
	f.Set("comp.totals.subtotals", "on")
	f.Set("comp.detail", "on")
	f.Set("comp.sort.0.field", "Сумма")
	f.Set("comp.sort.0.dir", "desc")
	f.Set("comp.cond.0.when", "Сумма < 0")
	f.Set("comp.cond.0.color", "#c00")
	f.Set("comp.cond.0.bold", "on")
	f.Set("comp.chart.type", "bar")
	f.Set("comp.chart.category", "Менеджер")
	f.Set("comp.chart.series", "Сумма")

	c, present := parseCompositionForm(f)
	if !present {
		t.Fatal("present=false")
	}
	if c == nil {
		t.Fatal("composition nil")
		return
	}
	if len(c.Groupings) != 2 || c.Groupings[1] != "Клиент" {
		t.Fatalf("groupings: %v", c.Groupings)
	}
	if len(c.Measures) != 1 || c.Measures[0].Agg != "sum" || c.Measures[0].Title != "Сумма, ₽" {
		t.Fatalf("measures: %+v", c.Measures)
	}
	if !c.Totals.Grand || !c.Totals.Subtotals || !c.Detail {
		t.Fatalf("totals/detail")
	}
	if len(c.Sort) != 1 || c.Sort[0].Dir != "desc" {
		t.Fatalf("sort: %+v", c.Sort)
	}
	if len(c.Conditional) != 1 || c.Conditional[0].Style.Color != "#c00" || !c.Conditional[0].Style.Bold {
		t.Fatalf("cond: %+v", c.Conditional)
	}
	if c.Chart == nil || c.Chart.Type != "bar" || c.Chart.Category != "Менеджер" || len(c.Chart.Series) != 1 {
		t.Fatalf("chart: %+v", c.Chart)
	}
}

func TestParseCompositionFormDefaultColors(t *testing.T) {
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.grouping.0", "М")
	f.Set("comp.cond.0.when", "X > 0")
	f.Set("comp.cond.0.color", "#000000")      // дефолт → пусто
	f.Set("comp.cond.0.background", "#ffffff") // дефолт → пусто
	f.Set("comp.cond.0.bold", "on")
	c, _ := parseCompositionForm(f)
	if c == nil || len(c.Conditional) != 1 {
		t.Fatalf("ожидали 1 правило: %+v", c)
	}
	s := c.Conditional[0].Style
	if s.Color != "" || s.Background != "" {
		t.Fatalf("дефолт-цвета должны стать пустыми: %+v", s)
	}
	if !s.Bold {
		t.Fatal("bold потерян")
	}
}

func TestParseCompositionFormCondOnlyPreserved(t *testing.T) {
	// Правило оформления без группировок/показателей не должно стираться.
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.cond.0.when", "Сумма < 0")
	f.Set("comp.cond.0.color", "#cc0000")
	c, present := parseCompositionForm(f)
	if !present {
		t.Fatal("present=false")
	}
	if c == nil || len(c.Conditional) != 1 {
		t.Fatalf("композиция с правилом должна сохраниться: %+v", c)
	}
}

func TestParseCompositionFormPartialDefaultColor(t *testing.T) {
	// Дефолт обнуляется независимо: color дефолтный, background — нет.
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.grouping.0", "М")
	f.Set("comp.cond.0.when", "X > 0")
	f.Set("comp.cond.0.color", "#000000")      // дефолт → пусто
	f.Set("comp.cond.0.background", "#123456") // не дефолт → остаётся
	c, _ := parseCompositionForm(f)
	s := c.Conditional[0].Style
	if s.Color != "" || s.Background != "#123456" {
		t.Fatalf("частичный дефолт: %+v", s)
	}
}

func TestParseCompositionFormAbsentAndEmpty(t *testing.T) {
	if c, present := parseCompositionForm(url.Values{}); present || c != nil {
		t.Fatalf("absent: present=%v c=%v", present, c)
	}
	f := url.Values{}
	f.Set("comp.present", "1")
	c, present := parseCompositionForm(f)
	if !present || c != nil {
		t.Fatalf("empty: present=%v c=%v (ждали present=true, c=nil)", present, c)
	}
}

func TestParseCompositionFormMeasureAlignFormat(t *testing.T) {
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.measure.0.field", "Сумма")
	f.Set("comp.measure.0.agg", "sum")
	f.Set("comp.measure.0.align", "center")
	f.Set("comp.measure.0.format", "#,##0.00")
	c, present := parseCompositionForm(f)
	if !present {
		t.Fatal("present=false")
	}
	if c == nil || len(c.Measures) != 1 {
		t.Fatalf("measures: %+v", c)
	}
	m := c.Measures[0]
	if m.Align != "center" {
		t.Fatalf("align: хотели %q, получили %q", "center", m.Align)
	}
	if m.Format != "#,##0.00" {
		t.Fatalf("format: хотели %q, получили %q", "#,##0.00", m.Format)
	}
}

func TestParseCompositionFormMeasureAlignFormatEmpty(t *testing.T) {
	// Пустые align и format не ломают существующую логику.
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.measure.0.field", "Количество")
	f.Set("comp.measure.0.agg", "count")
	// align и format не заданы
	c, present := parseCompositionForm(f)
	if !present || c == nil || len(c.Measures) != 1 {
		t.Fatalf("measures: %+v", c)
	}
	m := c.Measures[0]
	if m.Align != "" {
		t.Fatalf("align должен быть пустым: %q", m.Align)
	}
	if m.Format != "" {
		t.Fatalf("format должен быть пустым: %q", m.Format)
	}
}

func TestParseCompositionFormMeasureExpr(t *testing.T) {
	// Вычисляемый показатель: Expr задаётся через поле comp.measure.<i>.expr.
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.measure.0.field", "Выручка")
	f.Set("comp.measure.0.agg", "sum")
	f.Set("comp.measure.1.field", "Рентабельность")
	// Для вычисляемого показателя agg пуст, expr задан.
	f.Set("comp.measure.1.expr", "ВаловаяПрибыль / Выручка * 100")
	c, present := parseCompositionForm(f)
	if !present {
		t.Fatal("present=false")
	}
	if c == nil || len(c.Measures) != 2 {
		t.Fatalf("measures: %+v", c)
	}
	m0 := c.Measures[0]
	if m0.Field != "Выручка" || m0.Agg != "sum" || m0.Expr != "" {
		t.Fatalf("measure[0]: %+v", m0)
	}
	m1 := c.Measures[1]
	if m1.Field != "Рентабельность" || m1.Agg != "" || m1.Expr != "ВаловаяПрибыль / Выручка * 100" {
		t.Fatalf("measure[1] (вычисляемый): %+v", m1)
	}
}

func TestParseCompositionFormColumns(t *testing.T) {
	// Измерения-колонки кросс-таблицы читаются из полей comp.column.<i>.
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.grouping.0", "Номенклатура")
	f.Set("comp.column.0", "Месяц")
	f.Set("comp.column.1", "Склад")
	f.Set("comp.measure.0.field", "Сумма")
	f.Set("comp.measure.0.agg", "sum")
	c, present := parseCompositionForm(f)
	if !present || c == nil {
		t.Fatalf("present=%v c=%v", present, c)
	}
	if len(c.Columns) != 2 || c.Columns[0] != "Месяц" || c.Columns[1] != "Склад" {
		t.Fatalf("columns: %v", c.Columns)
	}
}

func TestParseCompositionFormColumnsOnlyPreserved(t *testing.T) {
	// Композиция с колонками и показателем, но без группировок, не должна стираться.
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.column.0", "Месяц")
	f.Set("comp.measure.0.field", "Сумма")
	f.Set("comp.measure.0.agg", "sum")
	c, present := parseCompositionForm(f)
	if !present || c == nil || len(c.Columns) != 1 {
		t.Fatalf("композиция с колонками должна сохраниться: present=%v c=%+v", present, c)
	}
}

func TestParseCompositionFormDetailLink(t *testing.T) {
	// DetailLink и DetailEntity читаются из формы и сохраняются в Composition.
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.grouping.0", "Контрагент")
	f.Set("comp.measure.0.field", "Сумма")
	f.Set("comp.measure.0.agg", "sum")
	f.Set("comp.detail", "on")
	f.Set("comp.detail_link", "Регистратор")
	f.Set("comp.detail_entity", "расходнаянакладная")

	c, present := parseCompositionForm(f)
	if !present {
		t.Fatal("present=false")
	}
	if c == nil {
		t.Fatal("composition nil")
		return
	}
	if c.DetailLink != "Регистратор" {
		t.Fatalf("DetailLink: хотели %q, получили %q", "Регистратор", c.DetailLink)
	}
	if c.DetailEntity != "расходнаянакладная" {
		t.Fatalf("DetailEntity: хотели %q, получили %q", "расходнаянакладная", c.DetailEntity)
	}
}

func TestParseCompositionFormDetailLinkEmpty(t *testing.T) {
	// Пустые detail_link и detail_entity не ломают существующую логику
	// и не попадают в условие очистки (composition с группировками сохраняется).
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.grouping.0", "М")
	f.Set("comp.measure.0.field", "Сумма")
	f.Set("comp.measure.0.agg", "sum")
	// detail_link и detail_entity не заданы

	c, present := parseCompositionForm(f)
	if !present || c == nil {
		t.Fatalf("без detail_link composition должна сохраниться: present=%v c=%v", present, c)
	}
	if c.DetailLink != "" || c.DetailEntity != "" {
		t.Fatalf("пустые поля должны остаться пустыми: link=%q entity=%q", c.DetailLink, c.DetailEntity)
	}
}

func TestApplyReportCompositionPreservesOtherFields(t *testing.T) {
	// Поля отчёта, не относящиеся к composition (мультиязычные titles, title,
	// params), не должны теряться при сохранении настроек композиции через
	// конфигуратор. Регресс: applyReportComposition round-trip'ил YAML через
	// структуру без поля Titles → titles молча удалялись (issue #86).
	raw := []byte("name: R\ntitle: Отчёт\ntitles:\n  en: Report\nquery: \"ВЫБРАТЬ 1\"\nparams:\n  - {name: Период, type: date}\n")

	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.grouping.0", "Менеджер")
	f.Set("comp.measure.0.field", "Сумма")
	f.Set("comp.measure.0.agg", "sum")

	out, err := applyReportComposition(raw, f)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "titles:") || !strings.Contains(s, "Report") {
		t.Fatalf("titles потеряны при сохранении composition:\n%s", s)
	}
	if !strings.Contains(s, "Отчёт") {
		t.Fatalf("title потерян:\n%s", s)
	}
	if !strings.Contains(s, "Период") {
		t.Fatalf("params потеряны:\n%s", s)
	}
	if !strings.Contains(s, "Менеджер") {
		t.Fatalf("новая composition не записана:\n%s", s)
	}
}

func TestApplyReportCompositionPreservesVariants(t *testing.T) {
	// Блок variants (варианты компоновки, C2) не должен теряться при сохранении
	// основного composition через конструктор конфигуратора — иначе сохранение
	// затирало бы пользовательские варианты.
	raw := []byte("name: R\nquery: \"ВЫБРАТЬ 1\"\ncomposition:\n  groupings: [Старое]\n  measures:\n    - {field: X, agg: sum}\nvariants:\n  - name: По складам\n    composition:\n      groupings: [Склад]\n      measures:\n        - {field: X, agg: sum}\n")

	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.grouping.0", "Новое")
	f.Set("comp.measure.0.field", "Сумма")
	f.Set("comp.measure.0.agg", "sum")

	out, err := applyReportComposition(raw, f)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "variants:") || !strings.Contains(s, "По складам") || !strings.Contains(s, "Склад") {
		t.Fatalf("variants потеряны при сохранении composition:\n%s", s)
	}
	if !strings.Contains(s, "Новое") {
		t.Fatalf("новая composition не записана:\n%s", s)
	}
}

func TestApplyReportComposition(t *testing.T) {
	raw := []byte("name: R\nquery: \"ВЫБРАТЬ 1\"\ncomposition:\n  groupings: [Старое]\n  measures:\n    - {field: X, agg: sum}\n")

	// форма без present → composition сохраняется как была
	out, err := applyReportComposition(raw, url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Старое") {
		t.Fatalf("composition должна сохраниться без present:\n%s", out)
	}

	// present + новые поля → перезапись
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.grouping.0", "Новое")
	f.Set("comp.measure.0.field", "Сумма")
	f.Set("comp.measure.0.agg", "sum")
	out, _ = applyReportComposition(raw, f)
	if !strings.Contains(string(out), "Новое") || strings.Contains(string(out), "Старое") {
		t.Fatalf("composition должна перезаписаться:\n%s", out)
	}

	// present, пусто → composition удаляется
	f2 := url.Values{}
	f2.Set("comp.present", "1")
	out, _ = applyReportComposition(raw, f2)
	if strings.Contains(string(out), "composition") {
		t.Fatalf("composition должна очиститься:\n%s", out)
	}
}
