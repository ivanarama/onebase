package ui

// report_cross_excel.go — экспорт кросс-таблицы (pivot) в Excel. Зеркалит
// renderCrossTable, но строит [][]any для excel.ExportList: шапка из строковых
// измерений и колонок, строки дерева с текстовым отступом, нижняя строка ВСЕГО.

import (
	"strings"

	"github.com/ivantit66/onebase/internal/excel"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
)

// crossSheetRows строит заголовки и строки данных для excel.ExportList из
// результата кросс-компоновки. Значения показателей проходят через
// measureCellForExcel (nil → пустая ячейка; формат показателя учитывается, как в
// обычной выгрузке), чтобы вид совпадал с HTML и Excel видел числа, а не текст.
func crossSheetRows(cr *compose.CrossResult, spec *report.Composition) (headers []string, rows [][]any) {
	headers, rows, _ = crossSheetRowsPresented(cr, spec, reportCompositionPresentation{})
	return headers, rows
}

func crossSheetRowsPresented(cr *compose.CrossResult, spec *report.Composition, presentation reportCompositionPresentation) (headers []string, rows [][]any, styles []excel.RowStyle) {
	// Показатель колонки берём по ИНДЕКСУ (CrossCol.MeasureIdx), а не по Field —
	// общий с HTML-рендером хелпер (issue #17), чтобы пути не расходились.
	measureAt := measureByIdx(spec)
	multiMeasure := len(spec.Measures) > 1

	headers = make([]string, 0, 1+len(cr.Cols))
	headers = append(headers, presentation.groupHeader(spec))
	for _, c := range cr.Cols {
		headers = append(headers, crossColTitle(c, measureAt(c), multiMeasure))
	}

	colCount := 1 + len(cr.Cols)
	var walk func(row *compose.CrossRow, level int)
	walk = func(row *compose.CrossRow, level int) {
		r := make([]any, colCount)
		r[0] = strings.Repeat("  ", level) + fmtVal(row.Key)
		for i, c := range cr.Cols {
			r[i+1] = measureCellForExcel(row.Cells[c.Key()], measureAt(c))
		}
		rows = append(rows, r)
		styles = append(styles, excel.RowStyle{Background: composedGroupFill(level, len(spec.Groupings)), Bold: true})
		for _, ch := range row.Children {
			walk(ch, level+1)
		}
	}
	for _, row := range cr.Rows {
		walk(row, 0)
	}

	tot := make([]any, colCount)
	tot[0] = presentation.grandTitle()
	for i, c := range cr.Cols {
		tot[i+1] = measureCellForExcel(cr.RowTotal[c.Key()], measureAt(c))
	}
	rows = append(rows, tot)
	styles = append(styles, excel.RowStyle{Bold: true})
	return headers, rows, styles
}
