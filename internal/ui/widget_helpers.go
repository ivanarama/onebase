package ui

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/widget"
	"github.com/shopspring/decimal"
)

// splitCamel turns "ПоступлениеТоваров" into "Поступление товаров" — a
// recent-widget pill should be readable, not a wall of mixed-case text. The
// first word keeps its capital; subsequent words become lowercase. Latin and
// Cyrillic both work because we look at Unicode properties, not byte ranges.
func splitCamel(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			if unicode.IsLower(prev) || unicode.IsDigit(prev) {
				b.WriteByte(' ')
				b.WriteRune(unicode.ToLower(r))
				continue
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

// widgetCell formats a single cell in a list-widget table according to the
// declared format (money/number/percent/date). It is wired into the template
// FuncMap as "wcell" and called like `{{wcell row "Field" "money"}}`.
func widgetCell(row map[string]any, field, format string) string {
	v, ok := row[field]
	if !ok || v == nil {
		return ""
	}
	switch strings.ToLower(format) {
	case "money":
		return formatMoneyForCell(v)
	case "number":
		return formatIntForCell(v)
	case "percent":
		f := toFloatForCell(v)
		return fmt.Sprintf("%.1f%%", f)
	case "date":
		if t, ok := v.(time.Time); ok {
			return t.Format("02.01.2006 15:04")
		}
		if s, ok := v.(string); ok && len(s) >= 10 {
			for _, layout := range []string{
				time.RFC3339, time.RFC3339Nano,
				"2006-01-02 15:04:05-07:00",
				"2006-01-02 15:04:05 -0700 MST",
				"2006-01-02 15:04:05.999999999 -0700 MST",
				"2006-01-02T15:04:05", "2006-01-02 15:04:05",
				"2006-01-02T15:04", "2006-01-02",
			} {
				if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
					h, m, sec := t.Clock()
					if h != 0 || m != 0 || sec != 0 {
						return t.Format("02.01.2006 15:04")
					}
					return t.Format("02.01.2006")
				}
			}
		}
		return fmt.Sprintf("%v", v)
	}
	return fmt.Sprintf("%v", v)
}

// echartsJSON serializes ChartData into an ECharts option payload, ready for
// JSON.parse on the client side. Returned as template.JS so html/template
// preserves the JSON unchanged inside <script>; the wrapping template emits it
// as a JavaScript expression, not an attribute, which avoids quote-escaping
// pitfalls.
func echartsJSON(chart *widget.ChartData) template.JS {
	opt := widget.EChartsOption(chart)
	if opt == nil {
		return template.JS("null")
	}
	b, err := json.Marshal(opt)
	if err != nil {
		return template.JS("null")
	}
	return template.JS(b) //nolint:gosec // G203: значение получено json.Marshal — он экранирует < > & в \u-последовательности, поэтому «</script>» из данных не разорвёт тег
}

func widgetChartsJSON(v any) template.JS {
	results, ok := v.([]widget.Result)
	if !ok || len(results) == 0 {
		return template.JS("{}")
	}
	out := make(map[string]any)
	for _, res := range results {
		if res.Type == "chart" && res.Chart != nil {
			out[res.Name] = widget.EChartsOption(res.Chart)
		}
	}
	return jsMarshal(out)
}

func pageChartsJSON(blocks []interpreter.PageBlock) template.JS {
	if len(blocks) == 0 {
		return template.JS("{}")
	}
	out := make(map[string]any)
	for i, block := range blocks {
		if block.Kind == "chart" && block.Chart != nil {
			out[fmt.Sprintf("%d", i)] = widget.EChartsOption(pageChartData(block.Chart))
		}
	}
	return jsMarshal(out)
}

func jsMarshal(v any) template.JS {
	b, err := json.Marshal(v)
	if err != nil {
		return template.JS("{}")
	}
	return template.JS(b) //nolint:gosec // G203: значение получено json.Marshal — он экранирует < > & в \u-последовательности, поэтому «</script>» из данных не разорвёт тег
}

func toFloatForCell(v any) float64 {
	switch t := v.(type) {
	case decimal.Decimal:
		return t.InexactFloat64()
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int32:
		return float64(t)
	case int64:
		return float64(t)
	case string:
		// Нечисловая строка даёт 0 — это и было прежним поведением, менять его
		// здесь нельзя: функция приводит произвольное значение виджета к числу
		// и вызывается на данных, где нечисловое значение штатно.
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return 0
		}
		return f
	}
	return 0
}

// formatMoneyForCell renders monetary values for table cells in list widgets.
// Cells are tight on horizontal space, so we drop kopecks and the currency
// glyph — the column header already conveys "Выручка"/"Прибыль", and one
// rouble precision is enough for a dashboard summary. KPI cards keep the full
// "x,xx ₽" form (see runner.formatMoney).
func formatMoneyForCell(v any) string {
	f := toFloatForCell(v)
	neg := f < 0
	if neg {
		f = -f
	}
	whole := int64(f + 0.5)
	s := groupThousands(whole)
	if neg {
		s = "-" + s
	}
	return s + " ₽"
}

func formatIntForCell(v any) string {
	f := toFloatForCell(v)
	neg := f < 0
	if neg {
		f = -f
	}
	whole := int64(f + 0.5)
	s := groupThousands(whole)
	if neg {
		return "-" + s
	}
	return s
}

func groupThousands(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	rem := len(s) % 3
	if rem > 0 {
		b.WriteString(s[:rem])
		if len(s) > rem {
			b.WriteByte(' ')
		}
	}
	for i := rem; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// fmtReportCell formats a single cell value in a report table. Unlike
// widgetCell (which takes a row + field + format), this receives the raw value
// directly and auto-detects the type: time.Time → date, float64 → number with
// thousands separators, strings that parse as dates → formatted date.
func fmtReportCell(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case time.Time:
		h, m, sec := t.Clock()
		if h != 0 || m != 0 || sec != 0 {
			return t.Format("02.01.2006 15:04:05")
		}
		return t.Format("02.01.2006")
	case decimal.Decimal:
		if t.IsInteger() {
			return groupThousands(t.IntPart())
		}
		return t.String()
	case float64:
		if t == float64(int64(t)) {
			return groupThousands(int64(t))
		}
		return fmt.Sprintf("%.2f", t)
	case float32:
		return fmt.Sprintf("%.2f", float64(t))
	case int:
		return groupThousands(int64(t))
	case int32:
		return groupThousands(int64(t))
	case int64:
		return groupThousands(t)
	case string:
		if len(t) >= 10 {
			for _, layout := range []string{
				time.RFC3339, "2006-01-02 15:04:05-07:00", "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02",
			} {
				if pt, err := time.Parse(layout, t); err == nil {
					h, m, sec := pt.Clock()
					if h != 0 || m != 0 || sec != 0 {
						return pt.Format("02.01.2006 15:04:05")
					}
					return pt.Format("02.01.2006")
				}
			}
		}
		return t
	}
	return fmt.Sprintf("%v", v)
}
