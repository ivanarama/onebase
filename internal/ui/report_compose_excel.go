package ui

// report_compose_excel.go — экспорт скомпонованного отчёта (СКД) в Excel.
// Обход дерева аналогичен renderComposedTable, но вместо HTML строит [][]any
// для передачи в excel.ExportList.

import (
	"strings"

	"github.com/ivantit66/onebase/internal/excel"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
	"github.com/ivantit66/onebase/internal/sheet"
)

// measureCellForExcel выбирает значение ячейки показателя для Excel так, чтобы
// она совпадала с HTML-рендером (fmtMeasure) и выгрузка не расходилась с экраном:
//   - nil → пустая ячейка;
//   - число с заданным Format → отформатированная строка (как FormatNumber в
//     HTML); иначе формат «0.0%» давал бы на экране «12,3%», а в XLSX 0.123;
//   - число без Format → float64 (Excel видит число, сортируемое);
//   - не-число (текстовый показатель в детали) → текст, а не ложный 0.
func measureCellForExcel(v any, m report.Measure) any {
	if v == nil {
		return nil
	}
	if d, ok := compose.ExportToDecimal(v); ok {
		if m.Format != "" {
			return compose.FormatNumber(d, m.Format)
		}
		f, _ := d.Float64()
		return f
	}
	return fmtVal(v)
}

// composedRows строит заголовки и строки данных для excel.ExportList из
// скомпонованного результата. Порядок строк задаёт walkComposed — общий с
// HTML-рендером (renderComposedTable), поэтому выгрузка и экран не расходятся:
//
//	группа с итогами → [дети/детали] … → Итого
//
// Первая колонка — имена/ключи групп с текстовым отступом (2 пробела × уровень).
// Значения показателей передаются как float64, чтобы Excel видел числа, а не текст.
func composedRows(res *compose.Result, spec *report.Composition) (headers []string, rows [][]any) {
	headers, rows, _ = composedRowsPresented(res, spec, reportCompositionPresentation{})
	return headers, rows
}

func composedRowsPresented(res *compose.Result, spec *report.Composition, presentation reportCompositionPresentation) (headers []string, rows [][]any, styles []excel.RowStyle) {
	headers = make([]string, 0, 1+len(spec.Measures))
	headers = append(headers, presentation.groupHeader(spec))
	for _, m := range spec.Measures {
		headers = append(headers, measureTitle(m))
	}
	sink := &excelComposeSink{spec: spec, colCount: 1 + len(spec.Measures), presentation: presentation}
	walkComposed(res, spec, sink)
	return headers, sink.rows, sink.rowStyles
}

// excelComposeSink собирает строки [][]any для excel.ExportList.
type excelComposeSink struct {
	spec         *report.Composition
	colCount     int
	rows         [][]any
	rowStyles    []excel.RowStyle
	presentation reportCompositionPresentation
}

// row добавляет строку: подпись в первой колонке + значения показателей
// (numForExcel: nil → пустая ячейка, иначе float64).
func (e *excelComposeSink) row(label string, vals map[string]any) {
	r := make([]any, e.colCount)
	r[0] = label
	for i, m := range e.spec.Measures {
		r[i+1] = measureCellForExcel(vals[m.Field], m)
	}
	e.rows = append(e.rows, r)
	e.rowStyles = append(e.rowStyles, excel.RowStyle{})
}

func (e *excelComposeSink) group(g *compose.Group, level int, _ string) {
	e.row(strings.Repeat("  ", level)+fmtVal(g.Key), g.Subtotals)
	fill := composedGroupFill(level, len(e.spec.Groupings))
	if explicit := sheet.ColorRGB(g.Styles[""].Background); explicit != "" {
		fill = "#" + explicit
	}
	e.rowStyles[len(e.rowStyles)-1] = excel.RowStyle{Background: fill, Bold: true}
}

func (e *excelComposeSink) detail(d compose.DetailRow, level int, _ string) {
	// Первая ячейка детали — только отступ (зеркалит HTML-рендер). Осмысленный
	// идентификатор строки (ссылка на документ) — задача B3 (detail_link).
	e.row(strings.Repeat("  ", level), d.Values)
}

func (e *excelComposeSink) grand(grand map[string]any) {
	e.row(e.presentation.grandTitle(), grand)
	e.rowStyles[len(e.rowStyles)-1] = excel.RowStyle{Bold: true}
}
