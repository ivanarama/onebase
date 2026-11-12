package ui

// report_compose_excel.go — экспорт скомпонованного отчёта (СКД) в Excel.
// Обход дерева аналогичен renderComposedTable, но вместо HTML строит [][]any
// для передачи в excel.ExportList.

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
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
//	группа → [дети/детали] → подытог … → ВСЕГО
//
// Первая колонка — имена/ключи групп с текстовым отступом (2 пробела × уровень).
// Значения показателей передаются как float64, чтобы Excel видел числа, а не текст.
func composedRows(res *compose.Result, spec *report.Composition) (headers []string, rows [][]any) {
	headers = make([]string, 0, 1+len(spec.DetailFields)+len(spec.Measures))
	headers = append(headers, strings.Join(spec.Groupings, " / "))
	for _, d := range spec.DetailFields {
		headers = append(headers, detailFieldTitle(d))
	}
	for _, m := range spec.Measures {
		headers = append(headers, measureTitle(m))
	}
	sink := &excelComposeSink{spec: spec, colCount: 1 + len(spec.DetailFields) + len(spec.Measures)}
	walkComposed(res, spec, sink)
	return headers, sink.rows
}

// excelComposeSink собирает строки [][]any для excel.ExportList.
type excelComposeSink struct {
	spec     *report.Composition
	colCount int
	rows     [][]any
}

// row добавляет строку: подпись в первой колонке + значения показателей
// (numForExcel: nil → пустая ячейка, иначе float64).
func (e *excelComposeSink) row(label string, vals map[string]any) {
	e.rowWithDetail(label, nil, vals)
}

// rowWithDetail — строка с полями детальной записи (detail != nil только у
// строки детализации; у групп и итогов эти колонки пусты).
func (e *excelComposeSink) rowWithDetail(label string, detail, vals map[string]any) {
	r := make([]any, e.colCount)
	r[0] = label
	off := 1
	for i, d := range e.spec.DetailFields {
		if detail == nil {
			continue
		}
		if _, ok := detailMoment(detail[d.Field]); ok {
			r[off+i] = fmtDetailVal(detail[d.Field])
		} else if v := detail[d.Field]; v != nil {
			r[off+i] = v
		}
	}
	off += len(e.spec.DetailFields)
	for i, m := range e.spec.Measures {
		r[off+i] = measureCellForExcel(vals[m.Field], m)
	}
	e.rows = append(e.rows, r)
}

func (e *excelComposeSink) group(g *compose.Group, level int, _ string) {
	e.row(strings.Repeat("  ", level)+fmtVal(g.Key), g.Subtotals)
}

func (e *excelComposeSink) detail(d compose.DetailRow, level int, _ string) {
	// Первая ячейка детали — только отступ (зеркалит HTML-рендер). Осмысленный
	// идентификатор строки (ссылка на документ) — задача B3 (detail_link).
	e.rowWithDetail(strings.Repeat("  ", level), d.Values, d.Values)
}

func (e *excelComposeSink) subtotal(g *compose.Group, level int, _ string) {
	e.row(fmt.Sprintf("%s··· Итого: %s ···", strings.Repeat("  ", level), fmtVal(g.Key)), g.Subtotals)
}

func (e *excelComposeSink) grand(grand map[string]any) {
	e.row("ВСЕГО", grand)
}
