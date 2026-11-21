package ui

import (
	"testing"

	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
)

// TestComposedRows проверяет порядок строк и содержимое ключевых ячеек.
//
// Структура: 2 группы (Иванов, Петров), итоги в строках групп, общий итог.
// Ожидаемый порядок (при Subtotals:true, Grand:true):
//
//	[0] заголовки     ("М / Отдел", "Сумма, ₽")
//	[1] группа Иванов (уровень 0)
//	[2] дочерняя      Отдел A (уровень 1)
//	[3] группа Петров (уровень 0)
//	[4] дочерняя      Отдел B (уровень 1)
//	[5] Итого
func TestComposedRows(t *testing.T) {
	rows := []compose.Row{
		{"М": "Иванов", "Отдел": "Отдел A", "Сумма": "100"},
		{"М": "Петров", "Отдел": "Отдел B", "Сумма": "50"},
	}
	spec := report.Composition{
		Groupings: []string{"М", "Отдел"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum", Title: "Сумма, ₽"}},
		Totals:    report.Totals{Grand: true, Subtotals: true},
	}
	res, err := compose.Compose(rows, spec, nil)
	if err != nil {
		t.Fatalf("compose error: %v", err)
	}

	headers, xlsRows := composedRows(res, &spec)

	// Проверяем заголовки
	if len(headers) != 2 {
		t.Fatalf("ожидали 2 заголовка, получили %d: %v", len(headers), headers)
	}
	if headers[0] != "М / Отдел" {
		t.Errorf("headers[0] = %q, ожидали %q", headers[0], "М / Отдел")
	}
	if headers[1] != "Сумма, ₽" {
		t.Errorf("headers[1] = %q, ожидали %q", headers[1], "Сумма, ₽")
	}

	// Подвалы не дублируют итоги даже при legacy Subtotals:true.
	wantRows := 5
	if len(xlsRows) != wantRows {
		t.Fatalf("ожидали %d строк, получили %d", wantRows, len(xlsRows))
	}

	// Строка 0: группа Иванов (уровень 0) — первая колонка с нулевым отступом
	row0 := xlsRows[0]
	if len(row0) != 2 {
		t.Fatalf("строка 0: ожидали 2 ячейки, получили %d", len(row0))
	}
	grpLabel0, _ := row0[0].(string)
	if grpLabel0 != "Иванов" {
		t.Errorf("строка 0 [0] = %q, ожидали %q", grpLabel0, "Иванов")
	}
	// Значение показателя должно быть числом (float64)
	if _, ok := row0[1].(float64); !ok {
		t.Errorf("строка 0 [1] должна быть float64, получили %T: %v", row0[1], row0[1])
	}

	// Строка 1: дочерняя группа «Отдел A» (уровень 1) — с отступом
	row1 := xlsRows[1]
	grpLabel1, _ := row1[0].(string)
	const indent = "  " // 2 пробела на уровень
	if grpLabel1 != indent+"Отдел A" {
		t.Errorf("строка 1 [0] = %q, ожидали %q", grpLabel1, indent+"Отдел A")
	}

	// Следом идут Петров и его дочерняя группа, без подвалов Иванова/Отдела A.
	row2 := xlsRows[2]
	sub2, _ := row2[0].(string)
	if sub2 != "Петров" {
		t.Errorf("строка 2 [0] = %q, ожидали Петров", sub2)
	}

	row3 := xlsRows[3]
	sub3, _ := row3[0].(string)
	if sub3 != indent+"Отдел B" {
		t.Errorf("строка 3 [0] = %q, ожидали %q", sub3, indent+"Отдел B")
	}

	// Последняя строка: Итого
	last := xlsRows[len(xlsRows)-1]
	grandLabel, _ := last[0].(string)
	if grandLabel != "Итого" {
		t.Errorf("последняя строка [0] = %q, ожидали %q", grandLabel, "Итого")
	}
	if _, ok := last[1].(float64); !ok {
		t.Errorf("последняя строка [1] должна быть float64, получили %T: %v", last[1], last[1])
	}
}

// TestComposedRowsDetail проверяет режим Detail=true: детальные строки идут сразу
// после строки группы; повторного подвала нет. Структура:
//
//	одна группировка «М» с двумя строками деталей, Subtotals:true, Grand:false.
//
// Ожидаемый порядок (3 строки данных):
//
//	[0] группа Иванов
//	[1] деталь 1 (первая ячейка = отступ-строка, показатель = float64)
//	[2] деталь 2
func TestComposedRowsDetail(t *testing.T) {
	rows := []compose.Row{
		{"М": "Иванов", "Сумма": "100"},
		{"М": "Иванов", "Сумма": "50"},
	}
	spec := report.Composition{
		Groupings: []string{"М"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum", Title: "Сумма, ₽"}},
		Totals:    report.Totals{Subtotals: true},
		Detail:    true,
	}
	res, err := compose.Compose(rows, spec, nil)
	if err != nil {
		t.Fatalf("compose error: %v", err)
	}

	_, xlsRows := composedRows(res, &spec)

	// Группа с итогом + 2 детали; Grand=false и подвалов нет.
	wantRows := 3
	if len(xlsRows) != wantRows {
		t.Fatalf("ожидали %d строк, получили %d: %v", wantRows, len(xlsRows), xlsRows)
	}

	// Строка 0: группа Иванов (уровень 0).
	grpLabel, _ := xlsRows[0][0].(string)
	if grpLabel != "Иванов" {
		t.Errorf("строка 0 [0] = %q, ожидали %q", grpLabel, "Иванов")
	}
	if _, ok := xlsRows[0][1].(float64); !ok {
		t.Errorf("строка 0 [1] должна быть float64, получили %T", xlsRows[0][1])
	}

	// Строки 1 и 2: детальные строки — первая ячейка только отступ, показатель float64.
	const detailIndent = "  " // level+1=1 → 2 пробела
	for i := 1; i <= 2; i++ {
		firstCell, _ := xlsRows[i][0].(string)
		if firstCell != detailIndent {
			t.Errorf("строка %d [0] = %q, ожидали отступ %q", i, firstCell, detailIndent)
		}
		if _, ok := xlsRows[i][1].(float64); !ok {
			t.Errorf("строка %d [1] должна быть float64, получили %T: %v", i, xlsRows[i][1], xlsRows[i][1])
		}
	}

	if xlsRows[0][1] != float64(150) {
		t.Errorf("итог группы должен оставаться в её заголовке: %v", xlsRows[0])
	}
}

// TestComposedRowsNilMeasure проверяет, что nil-значение показателя (например,
// min/max/avg на пустой группе) попадает в ячейку как nil, а не float64(0).
// Это обеспечивает соответствие Excel-выгрузки HTML-рендеру, где nil → пустая ячейка.
func TestComposedRowsNilMeasure(t *testing.T) {
	// Собираем Result вручную: одна группа с nil-подытогом (имитирует avg на пустой группе)
	// и nil в Grand.
	res := &compose.Result{
		Groups: []*compose.Group{
			{
				Key:       "Группа1",
				Subtotals: map[string]any{"Кол": nil},
			},
		},
		Grand: map[string]any{"Кол": nil},
	}
	spec := &report.Composition{
		Groupings: []string{"М"},
		Measures:  []report.Measure{{Field: "Кол", Agg: "avg", Title: "Кол-во"}},
		Totals:    report.Totals{Grand: true, Subtotals: true},
	}

	_, xlsRows := composedRows(res, spec)

	// Группа с итогом + общий итог; повторного подвала нет.
	if len(xlsRows) != 2 {
		t.Fatalf("ожидали 2 строки, получили %d: %v", len(xlsRows), xlsRows)
	}

	// Строка группы [0][1] должна быть nil, а не float64(0).
	if xlsRows[0][1] != nil {
		t.Errorf("строка группы [1]: ожидали nil (пустая ячейка), получили %T(%v)", xlsRows[0][1], xlsRows[0][1])
	}

	// Строка общего итога [1][1] должна быть nil.
	if xlsRows[1][1] != nil {
		t.Errorf("строка общего итога [1]: ожидали nil, получили %T(%v)", xlsRows[1][1], xlsRows[1][1])
	}
}

// TestComposedRowsFlat проверяет однуровневую группировку без деталей.
func TestComposedRowsFlat(t *testing.T) {
	rows := []compose.Row{
		{"М": "Иванов", "Сумма": "150"},
		{"М": "Петров", "Сумма": "30"},
	}
	spec := report.Composition{
		Groupings: []string{"М"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Totals:    report.Totals{Grand: true},
	}
	res, _ := compose.Compose(rows, spec, nil)
	headers, xlsRows := composedRows(res, &spec)

	if headers[0] != "М" {
		t.Errorf("headers[0] = %q, ожидали %q", headers[0], "М")
	}
	if headers[1] != "Сумма" {
		t.Errorf("headers[1] = %q, ожидали %q", headers[1], "Сумма")
	}

	// 2 группы + общий итог = 3 строки (без подытогов)
	if len(xlsRows) != 3 {
		t.Fatalf("ожидали 3 строки, получили %d", len(xlsRows))
	}
	last := xlsRows[2]
	if s, _ := last[0].(string); s != "Итого" {
		t.Errorf("строка общего итога: got %q", s)
	}
}
