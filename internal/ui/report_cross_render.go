package ui

// report_cross_render.go — HTML-рендер кросс-таблицы (pivot). Строковые измерения
// уходят влево, значения измерений-колонок — в шапку, на пересечении выводятся
// агрегаты показателей. Выравнивание, формат и условное оформление — общие с
// обычным режимом (measureAlign/fmtMeasure/cssOf), чтобы вид был единым.

import (
	"fmt"
	"html"
	"html/template"
	"strings"

	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
)

// measureByIdx возвращает функцию выбора показателя колонки по CrossCol.MeasureIdx.
// Два показателя с одинаковым Field — разные колонки (issue #17): сопоставление по
// индексу не даёт им затирать подпись/формат/выравнивание друг друга. Общая для
// HTML- (renderCrossTable) и Excel-рендера (crossSheetRows), чтобы логика не
// разъезжалась — именно расхождение этих двух путей и было причиной #17.
func measureByIdx(spec *report.Composition) func(compose.CrossCol) report.Measure {
	byIdx := spec.Measures
	return func(c compose.CrossCol) report.Measure {
		if c.MeasureIdx >= 0 && c.MeasureIdx < len(byIdx) {
			return byIdx[c.MeasureIdx]
		}
		return report.Measure{Field: c.Measure}
	}
}

func renderCrossTable(cr *compose.CrossResult, spec *report.Composition) template.HTML {
	measureAt := measureByIdx(spec)
	multiMeasure := len(spec.Measures) > 1

	var b strings.Builder
	b.WriteString(`<table class="` + appearanceClass(spec) + ` report-cross">`)

	// Шапка: первый столбец — строковые измерения; далее по колонке на CrossCol.
	b.WriteString(`<thead><tr><th>` + html.EscapeString(strings.Join(spec.Groupings, " / ")) + `</th>`)
	for _, c := range cr.Cols {
		m := measureAt(c)
		b.WriteString(`<th class="num" style="` + html.EscapeString(measureAlign(m)) + `">` +
			html.EscapeString(crossColTitle(c, m, multiMeasure)) + `</th>`)
	}
	b.WriteString(`</tr></thead><tbody>`)

	// Тело: дерево строк.
	for _, row := range cr.Rows {
		writeCrossRow(&b, row, cr.Cols, measureAt, 0)
	}

	// Нижняя строка ВСЕГО — итоги по колонкам.
	b.WriteString(`<tr class="grand"><td>ВСЕГО</td>`)
	for _, c := range cr.Cols {
		m := measureAt(c)
		b.WriteString(`<td class="num" style="` + html.EscapeString(measureAlign(m)) + `">` +
			html.EscapeString(fmtMeasure(cr.RowTotal[c.Key()], m)) + `</td>`)
	}
	b.WriteString(`</tr></tbody></table>`)
	return template.HTML(b.String()) //nolint:gosec // G203: HTML собран с html.EscapeString на каждом значении
}

// writeCrossRow рисует строку дерева и рекурсивно её детей с нарастающим отступом.
// Узлы с детьми (промежуточные группы) несут подытоги по колонкам.
func writeCrossRow(b *strings.Builder, row *compose.CrossRow, cols []compose.CrossCol, measureAt func(compose.CrossCol) report.Measure, level int) {
	pad := fmt.Sprintf("padding-left:%dpx", 8+level*18)
	cls := "rc-leaf"
	if len(row.Children) > 0 {
		cls = "rc-group"
	}
	fmt.Fprintf(b, `<tr class="%s"><td style="%s">%s</td>`, cls, pad, html.EscapeString(fmtVal(row.Key)))
	for _, c := range cols {
		m := measureAt(c)
		ck := c.Key()
		cell := joinStyles(measureAlign(m), cssOf(row.Styles[ck]))
		fmt.Fprintf(b, `<td class="num" style="%s">%s</td>`, html.EscapeString(cell), html.EscapeString(fmtMeasure(row.Cells[ck], m)))
	}
	b.WriteString(`</tr>`)
	for _, ch := range row.Children {
		writeCrossRow(b, ch, cols, measureAt, level+1)
	}
}

// crossColTitle — подпись колонки: значение(я) пути через « / »; при нескольких
// показателях добавляется название показателя, чтобы колонки различались.
func crossColTitle(c compose.CrossCol, m report.Measure, multiMeasure bool) string {
	parts := make([]string, len(c.Path))
	for i, p := range c.Path {
		parts[i] = fmtVal(p)
	}
	title := strings.Join(parts, " / ")
	if multiMeasure {
		title += " · " + measureTitle(m)
	}
	return title
}
