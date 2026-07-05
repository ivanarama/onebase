package ui

import (
	"fmt"
	"html"
	"html/template"
	"net/url"
	"strings"

	"github.com/ivantit66/onebase/internal/csssafe"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
	"github.com/ivantit66/onebase/internal/widget"
)

// renderComposedTable строит единую <table> с раскрываемыми группами,
// подитогами, общим итогом и условным оформлением деталей. Порядок строк задаёт
// walkComposed (общий с Excel-выгрузкой); htmlComposeSink рисует каждую строку.
func renderComposedTable(res *compose.Result, spec *report.Composition) template.HTML {
	var b strings.Builder
	b.WriteString(`<table class="` + appearanceClass(spec) + `">`)
	b.WriteString(`<thead><tr><th>` + html.EscapeString(strings.Join(spec.Groupings, " / ")) + `</th>`)
	for _, m := range spec.Measures {
		b.WriteString(`<th class="num" style="` + html.EscapeString(measureAlign(m)) + `">` + html.EscapeString(measureTitle(m)) + `</th>`)
	}
	b.WriteString(`</tr></thead><tbody>`)
	walkComposed(res, spec, &htmlComposeSink{b: &b, spec: spec})
	b.WriteString(`</tbody></table>`)
	return template.HTML(b.String())
}

// htmlComposeSink рисует строки скомпонованного отчёта в HTML-таблицу.
type htmlComposeSink struct {
	b    *strings.Builder
	spec *report.Composition
}

// measureCells выводит ячейки показателей строки: выравнивание + условное
// оформление по styles[поле]. Общий код для строк группы, подытога и детали
// (раньше один и тот же цикл был скопирован трижды).
func (h *htmlComposeSink) measureCells(vals map[string]any, styles map[string]report.CellStyle) {
	for _, m := range h.spec.Measures {
		cell := joinStyles(measureAlign(m), cssOf(styles[m.Field]))
		h.b.WriteString(`<td class="num" style="` + html.EscapeString(cell) + `">` + html.EscapeString(fmtMeasure(vals[m.Field], m)) + `</td>`)
	}
}

func (h *htmlComposeSink) group(g *compose.Group, level int, path string) {
	pad := fmt.Sprintf("padding-left:%dpx", 8+level*18)
	rowStyle := cssOf(g.Styles[""])
	fmt.Fprintf(h.b, `<tr class="grp" data-group="%s" data-level="%d" style="%s"><td style="%s">▼ %s</td>`,
		html.EscapeString(path), level, html.EscapeString(rowStyle), pad, html.EscapeString(fmtVal(g.Key)))
	h.measureCells(g.Subtotals, g.Styles)
	h.b.WriteString(`</tr>`)
}

func (h *htmlComposeSink) detail(d compose.DetailRow, level int, path string) {
	rowStyle := cssOf(d.Styles[""])
	fmt.Fprintf(h.b, `<tr class="det" data-parent="%s" style="%s">`, html.EscapeString(path), html.EscapeString(rowStyle))
	// Первая ячейка: ссылка-расшифровка на исходный документ (если настроено).
	// Ссылка строится только когда заданы DetailLink, DetailEntity и значение поля
	// непустое. Без DetailEntity переход бессмыслен — выводим пустую ячейку.
	linked := false
	if h.spec.DetailLink != "" && h.spec.DetailEntity != "" {
		if v := fmtVal(d.Values[h.spec.DetailLink]); v != "" {
			href := "/ui/document/" + url.PathEscape(h.spec.DetailEntity) + "/" + url.PathEscape(v)
			fmt.Fprintf(h.b, `<td style="padding-left:%dpx"><a href="%s">→</a></td>`, 8+level*18, html.EscapeString(href))
			linked = true
		}
	}
	if !linked {
		fmt.Fprintf(h.b, `<td style="padding-left:%dpx"></td>`, 8+level*18)
	}
	h.measureCells(d.Values, d.Styles)
	h.b.WriteString(`</tr>`)
}

func (h *htmlComposeSink) subtotal(g *compose.Group, level int, path string) {
	rowStyle := cssOf(g.Styles[""])
	fmt.Fprintf(h.b, `<tr class="subtotal" data-parent="%s" style="%s"><td style="padding-left:%dpx">··· Итого: %s ···</td>`,
		html.EscapeString(path), html.EscapeString(rowStyle), 8+level*18, html.EscapeString(fmtVal(g.Key)))
	h.measureCells(g.Subtotals, g.Styles)
	h.b.WriteString(`</tr>`)
}

func (h *htmlComposeSink) grand(grand map[string]any) {
	// У общего итога нет условного оформления — measureCells с nil-стилями даёт
	// те же ячейки (только выравнивание), что и прежний отдельный код.
	h.b.WriteString(`<tr class="grand"><td>ВСЕГО</td>`)
	h.measureCells(grand, nil)
	h.b.WriteString(`</tr>`)
}

func cssOf(s report.CellStyle) string {
	return cssStyle(s.Color, s.Background, s.Bold, s.Italic)
}

func cssStyle(color, background string, bold, italic bool) string {
	var p []string
	if c := csssafe.Color(color); c != "" {
		p = append(p, "color:"+c)
	}
	if c := csssafe.Color(background); c != "" {
		p = append(p, "background:"+c)
	}
	if bold {
		p = append(p, "font-weight:bold")
	}
	if italic {
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

// measureAlign возвращает CSS-свойство выравнивания для ячейки показателя.
// По умолчанию (пустое Align) — вправо, как было исторически.
func measureAlign(m report.Measure) string {
	switch m.Align {
	case "left", "center":
		return "text-align:" + m.Align
	default:
		return "text-align:right"
	}
}

// appearanceClass возвращает CSS-классы таблицы отчёта по оформлению компоновки
// (план 71). База — "report-composed"; доп. классы выбираются switch'ем по
// фиксированным именам, поэтому недоверенное значение Appearance.Lines (приходит
// и из __settings) не попадает в разметку произвольной строкой. "" и "horizontal"
// → без доп. класса (исторический вид: только нижние границы).
func appearanceClass(spec *report.Composition) string {
	cls := "report-composed"
	if spec == nil {
		return cls
	}
	switch spec.Appearance.Lines {
	case "vertical":
		cls += " rep-lines-v"
	case "both":
		cls += " rep-lines-both"
	case "none":
		cls += " rep-lines-none"
	}
	if spec.Appearance.Zebra {
		cls += " rep-zebra"
	}
	return cls
}

// joinStyles объединяет два CSS-стиля через ";", пропуская пустые части.
func joinStyles(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + ";" + b
}

// buildComposedChart строит ECharts-option через общий widget.EChartsOption —
// единый вид с виджетами дашборда и предпросмотром (grid, сглаживание линий,
// radius круговой). Ось категорий задаёт Chart.Category: если оно пустое или
// совпадает с верхней группировкой — берём ключи верхнего уровня (как раньше);
// иначе сворачиваем плоские строки rows отдельным проходом по полю Category
// (раньше Category игнорировался и ось всегда шла по группировке).
func buildComposedChart(res *compose.Result, c *report.ChartSpec, rows []compose.Row, spec report.Composition, ev compose.Evaluator) map[string]any {
	if c == nil {
		return nil
	}
	cd := &widget.ChartData{Kind: c.Type}
	fields := seriesFields(c)

	byGrouping := c.Category == "" || (len(spec.Groupings) > 0 && c.Category == spec.Groupings[0])
	if byGrouping {
		if len(res.Groups) == 0 {
			return nil
		}
		for _, g := range res.Groups {
			cd.XAxis = append(cd.XAxis, fmtVal(g.Key))
		}
		for _, sf := range fields {
			s := widget.ChartSeries{Name: sf}
			for _, g := range res.Groups {
				s.Data = append(s.Data, composeFloat(g.Subtotals[sf]))
			}
			cd.Series = append(cd.Series, s)
		}
		return widget.EChartsOption(cd)
	}

	// Category != верхней группировки: свод по произвольному полю. Соблюдаем тот
	// же потолок строк, что и таблица (res.RowCount = число строк после cap),
	// иначе диаграмма агрегировала бы больше данных, чем показано в отчёте.
	if len(rows) > res.RowCount {
		rows = rows[:res.RowCount]
	}
	keys, subtotals := compose.GroupByField(rows, spec, c.Category, ev)
	if len(keys) == 0 {
		return nil
	}
	for _, k := range keys {
		cd.XAxis = append(cd.XAxis, fmtVal(k))
	}
	for _, sf := range fields {
		s := widget.ChartSeries{Name: sf}
		for _, k := range keys {
			s.Data = append(s.Data, composeFloat(subtotals[k][sf]))
		}
		cd.Series = append(cd.Series, s)
	}
	return widget.EChartsOption(cd)
}

// seriesFields выбирает поля рядов: круговая — один ряд по первому показателю,
// столбцы/линия — все ряды.
func seriesFields(c *report.ChartSpec) []string {
	if c.Type == "pie" {
		if sf := firstSeries(c); sf != "" {
			return []string{sf}
		}
		return nil
	}
	return c.Series
}

func firstSeries(c *report.ChartSpec) string {
	if len(c.Series) > 0 {
		return c.Series[0]
	}
	return ""
}

// composeFloat приводит значение показателя к float64 для графика и Excel.
func composeFloat(v any) float64 {
	if d, ok := compose.ExportToDecimal(v); ok {
		f, _ := d.Float64()
		return f
	}
	return 0
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

// fmtMeasure форматирует значение показателя с учётом поля Format.
// Если Format пустой или значение не числовое — возвращает fmtVal(v).
func fmtMeasure(v any, m report.Measure) string {
	if m.Format != "" {
		if d, ok := compose.ExportToDecimal(v); ok {
			return compose.FormatNumber(d, m.Format)
		}
	}
	return fmtVal(v)
}

// pathSeg экранирует сегмент пути группы для data-group/data-parent. Без этого
// «/» внутри значения группировки ломает префиксное сопоставление при
// сворачивании: сиблинг «A/Б» (data-group "/A/Б") ложно прятался при
// сворачивании «A» (селектор [data-group^="/A/"]). Видимая подпись — сырая.
func pathSeg(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	return strings.ReplaceAll(s, "/", "%2F")
}
