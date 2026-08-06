package compform_test

import (
	"net/url"
	"testing"

	"github.com/ivantit66/onebase/internal/report/compform"
)

func TestParse(t *testing.T) {
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

	c, present := compform.Parse(f)
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

func TestParseDefaultColors(t *testing.T) {
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.grouping.0", "М")
	f.Set("comp.cond.0.when", "X > 0")
	f.Set("comp.cond.0.color", "#000000")      // дефолт → пусто
	f.Set("comp.cond.0.background", "#ffffff") // дефолт → пусто
	f.Set("comp.cond.0.bold", "on")
	c, _ := compform.Parse(f)
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

func TestParseCondOnlyPreserved(t *testing.T) {
	// Правило оформления без группировок/показателей не должно стираться.
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.cond.0.when", "Сумма < 0")
	f.Set("comp.cond.0.color", "#cc0000")
	c, present := compform.Parse(f)
	if !present {
		t.Fatal("present=false")
	}
	if c == nil || len(c.Conditional) != 1 {
		t.Fatalf("композиция с правилом должна сохраниться: %+v", c)
	}
}

func TestParsePartialDefaultColor(t *testing.T) {
	// Дефолт обнуляется независимо: color дефолтный, background — нет.
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.grouping.0", "М")
	f.Set("comp.cond.0.when", "X > 0")
	f.Set("comp.cond.0.color", "#000000")      // дефолт → пусто
	f.Set("comp.cond.0.background", "#123456") // не дефолт → остаётся
	c, _ := compform.Parse(f)
	s := c.Conditional[0].Style
	if s.Color != "" || s.Background != "#123456" {
		t.Fatalf("частичный дефолт: %+v", s)
	}
}

func TestParseAbsentAndEmpty(t *testing.T) {
	if c, present := compform.Parse(url.Values{}); present || c != nil {
		t.Fatalf("absent: present=%v c=%v", present, c)
	}
	f := url.Values{}
	f.Set("comp.present", "1")
	c, present := compform.Parse(f)
	if !present || c != nil {
		t.Fatalf("empty: present=%v c=%v (ждали present=true, c=nil)", present, c)
	}
}

func TestParseMeasureAlignFormat(t *testing.T) {
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.measure.0.field", "Сумма")
	f.Set("comp.measure.0.agg", "sum")
	f.Set("comp.measure.0.align", "center")
	f.Set("comp.measure.0.format", "#,##0.00")
	c, present := compform.Parse(f)
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

func TestParseMeasureAlignFormatEmpty(t *testing.T) {
	// Пустые align и format не ломают существующую логику.
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.measure.0.field", "Количество")
	f.Set("comp.measure.0.agg", "count")
	c, present := compform.Parse(f)
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

func TestParseMeasureExpr(t *testing.T) {
	// Вычисляемый показатель: Expr задаётся через поле comp.measure.<i>.expr.
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.measure.0.field", "Выручка")
	f.Set("comp.measure.0.agg", "sum")
	f.Set("comp.measure.1.field", "Рентабельность")
	f.Set("comp.measure.1.expr", "ВаловаяПрибыль / Выручка * 100")
	c, present := compform.Parse(f)
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

func TestParseColumns(t *testing.T) {
	// Измерения-колонки кросс-таблицы читаются из полей comp.column.<i>.
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.grouping.0", "Номенклатура")
	f.Set("comp.column.0", "Месяц")
	f.Set("comp.column.1", "Склад")
	f.Set("comp.measure.0.field", "Сумма")
	f.Set("comp.measure.0.agg", "sum")
	c, present := compform.Parse(f)
	if !present || c == nil {
		t.Fatalf("present=%v c=%v", present, c)
	}
	if len(c.Columns) != 2 || c.Columns[0] != "Месяц" || c.Columns[1] != "Склад" {
		t.Fatalf("columns: %v", c.Columns)
	}
}

func TestParseColumnsOnlyPreserved(t *testing.T) {
	// Композиция с колонками и показателем, но без группировок, не должна стираться.
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.column.0", "Месяц")
	f.Set("comp.measure.0.field", "Сумма")
	f.Set("comp.measure.0.agg", "sum")
	c, present := compform.Parse(f)
	if !present || c == nil || len(c.Columns) != 1 {
		t.Fatalf("композиция с колонками должна сохраниться: present=%v c=%+v", present, c)
	}
}

func TestParseDetailLink(t *testing.T) {
	// DetailLink и DetailEntity читаются из формы и сохраняются в Composition.
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.grouping.0", "Контрагент")
	f.Set("comp.measure.0.field", "Сумма")
	f.Set("comp.measure.0.agg", "sum")
	f.Set("comp.detail", "on")
	f.Set("comp.detail_link", "Регистратор")
	f.Set("comp.detail_entity", "расходнаянакладная")

	c, present := compform.Parse(f)
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

func TestParseAppearance(t *testing.T) {
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.grouping.0", "М")
	f.Set("comp.measure.0.field", "Сумма")
	f.Set("comp.appearance.lines", "both")
	f.Set("comp.appearance.zebra", "on")
	c, present := compform.Parse(f)
	if !present || c == nil {
		t.Fatalf("present=%v c=%v", present, c)
	}
	if c.Appearance.Lines != "both" || !c.Appearance.Zebra {
		t.Fatalf("appearance: %+v", c.Appearance)
	}
}

func TestParseAppearanceGarbageLines(t *testing.T) {
	// Недоверенное значение lines (как из __settings) нормализуется к "" — в
	// CSS-класс рендера произвольная строка не просачивается.
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.grouping.0", "М")
	f.Set("comp.measure.0.field", "Сумма")
	f.Set("comp.appearance.lines", "vertical; } body{display:none")
	c, _ := compform.Parse(f)
	if c == nil || c.Appearance.Lines != "" {
		t.Fatalf("мусорный lines должен стать пустым: %+v", c)
	}
}

// comp.appearance.collapse_to: пустое поле — «не задано» (nil, отчёт открывается
// развёрнутым), ноль — значимое значение, мусор игнорируется, отрицательное
// сводится к нулю (issue #575).
func TestParseAppearanceCollapseTo(t *testing.T) {
	base := func() url.Values {
		f := url.Values{}
		f.Set("comp.present", "1")
		f.Set("comp.grouping.0", "М")
		f.Set("comp.measure.0.field", "Сумма")
		return f
	}
	cases := []struct {
		in   string
		want *int
	}{
		{"", nil},
		{"0", ptr(0)},
		{"2", ptr(2)},
		{" 3 ", ptr(3)},
		{"-1", ptr(0)},
		{"мусор", nil},
	}
	for _, c := range cases {
		f := base()
		f.Set("comp.appearance.collapse_to", c.in)
		got, _ := compform.Parse(f)
		if got == nil {
			t.Fatalf("collapse_to=%q: композиция не разобрана", c.in)
		}
		switch {
		case c.want == nil && got.Appearance.CollapseTo != nil:
			t.Errorf("collapse_to=%q: ожидали nil, получили %d", c.in, *got.Appearance.CollapseTo)
		case c.want != nil && got.Appearance.CollapseTo == nil:
			t.Errorf("collapse_to=%q: ожидали %d, получили nil", c.in, *c.want)
		case c.want != nil && *got.Appearance.CollapseTo != *c.want:
			t.Errorf("collapse_to=%q: ожидали %d, получили %d", c.in, *c.want, *got.Appearance.CollapseTo)
		}
	}
}

func ptr(v int) *int { return &v }

func TestParseAppearanceOnlyCleared(t *testing.T) {
	// Только оформление, без группировок/показателей/правил → композиция считается
	// пустой (nil, true): оформление само по себе её непустой не делает.
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.appearance.lines", "both")
	f.Set("comp.appearance.zebra", "on")
	c, present := compform.Parse(f)
	if !present || c != nil {
		t.Fatalf("только appearance: ждали present=true, c=nil; получили present=%v c=%+v", present, c)
	}
}

func TestParseDetailLinkEmpty(t *testing.T) {
	// Пустые detail_link и detail_entity не ломают существующую логику
	// и не попадают в условие очистки (composition с группировками сохраняется).
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.grouping.0", "М")
	f.Set("comp.measure.0.field", "Сумма")
	f.Set("comp.measure.0.agg", "sum")

	c, present := compform.Parse(f)
	if !present || c == nil {
		t.Fatalf("без detail_link composition должна сохраниться: present=%v c=%v", present, c)
	}
	if c.DetailLink != "" || c.DetailEntity != "" {
		t.Fatalf("пустые поля должны остаться пустыми: link=%q entity=%q", c.DetailLink, c.DetailEntity)
	}
}
