package compose

import (
	"testing"

	"github.com/ivantit66/onebase/internal/report"
)

func TestCrossBasic(t *testing.T) {
	// Кросс-таблица: строки — Товар, колонки — Месяц, на пересечении — Сумма.
	rows := []Row{
		{"Товар": "А", "Месяц": "Янв", "Сумма": "100"},
		{"Товар": "А", "Месяц": "Фев", "Сумма": "200"},
		{"Товар": "Б", "Месяц": "Янв", "Сумма": "30"},
		{"Товар": "Б", "Месяц": "Фев", "Сумма": "40"},
	}
	spec := report.Composition{
		Groupings: []string{"Товар"},
		Columns:   []string{"Месяц"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
	}
	cr, err := ComposeCross(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cr.Cols) != 2 {
		t.Fatalf("ожидали 2 колонки (Янв, Фев), получили %d", len(cr.Cols))
	}
	if len(cr.Rows) != 2 {
		t.Fatalf("ожидали 2 строки (А, Б), получили %d", len(cr.Rows))
	}
	var rowA *CrossRow
	for _, r := range cr.Rows {
		if r.Key == "А" {
			rowA = r
		}
	}
	if rowA == nil {
		t.Fatal("строка «А» не найдена")
		return
	}
	decEq(t, rowA.Cells[colKey([]any{"Янв"}, 0)], "100")
	decEq(t, rowA.Cells[colKey([]any{"Фев"}, 0)], "200")
	// Нижняя строка ВСЕГО: итог по колонке = сумма по всем строкам.
	decEq(t, cr.RowTotal[colKey([]any{"Янв"}, 0)], "130")
	decEq(t, cr.RowTotal[colKey([]any{"Фев"}, 0)], "240")
}

func TestCrossCap(t *testing.T) {
	// Потолок строк обрезает входные строки и выставляет Capped (как ComposeN).
	rows := []Row{
		{"Товар": "А", "Месяц": "Янв", "Сумма": "10"},
		{"Товар": "Б", "Месяц": "Янв", "Сумма": "20"},
		{"Товар": "В", "Месяц": "Янв", "Сумма": "30"},
	}
	spec := report.Composition{
		Groupings: []string{"Товар"},
		Columns:   []string{"Месяц"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
	}
	cr, err := ComposeCrossN(rows, spec, noEval{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !cr.Capped || cr.RowCount != 2 {
		t.Fatalf("ожидали потолок 2: capped=%v rowcount=%d", cr.Capped, cr.RowCount)
	}
	if len(cr.Rows) != 2 {
		t.Fatalf("после обрезки до 2 строк ожидали 2 группы, получили %d", len(cr.Rows))
	}
}

func TestCrossCellStyles(t *testing.T) {
	// Условное оформление применяется поячеечно: ячейка с убытком подсвечивается,
	// прибыльная — нет (правило When="Сумма < 0" на всю строку).
	rows := []Row{
		{"Товар": "А", "Месяц": "Янв", "Сумма": "-50"},
		{"Товар": "А", "Месяц": "Фев", "Сумма": "200"},
	}
	spec := report.Composition{
		Groupings: []string{"Товар"},
		Columns:   []string{"Месяц"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Conditional: []report.CondRule{
			{When: "Сумма < 0", Field: "", Style: report.CellStyle{Color: "#c00", Bold: true}},
		},
	}
	cr, err := ComposeCross(rows, spec, negEval{})
	if err != nil {
		t.Fatal(err)
	}
	rowA := cr.Rows[0]
	st := rowA.Styles[colKey([]any{"Янв"}, 0)]
	if st.Color != "#c00" || !st.Bold {
		t.Fatalf("ячейка Янв (убыток) должна быть стилизована: %+v", st)
	}
	if _, ok := rowA.Styles[colKey([]any{"Фев"}, 0)]; ok {
		t.Fatalf("ячейка Фев (прибыль) не должна иметь стиль")
	}
}

func TestCrossColumnsSorted(t *testing.T) {
	// Колонки упорядочены по значению пути, а не по порядку появления в данных —
	// иначе порядок колонок зависел бы от того, какая строка пришла первой.
	rows := []Row{
		{"Товар": "А", "Месяц": "3", "Сумма": "30"},
		{"Товар": "А", "Месяц": "1", "Сумма": "10"},
		{"Товар": "А", "Месяц": "2", "Сумма": "20"},
	}
	spec := report.Composition{
		Groupings: []string{"Товар"},
		Columns:   []string{"Месяц"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
	}
	cr, err := ComposeCross(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cr.Cols) != 3 {
		t.Fatalf("ожидали 3 колонки, получили %d", len(cr.Cols))
	}
	want := []string{"1", "2", "3"}
	for i, c := range cr.Cols {
		if toStr(c.Path[0]) != want[i] {
			t.Fatalf("колонка %d: ожидали %s, получили %v", i, want[i], c.Path[0])
		}
	}
}

func TestCrossNestedRows(t *testing.T) {
	// Многоуровневые строки: дерево групп; промежуточный узел несёт подытоги по
	// колонкам. Пропущенная ячейка (у группы нет строк в колонке) отсутствует.
	rows := []Row{
		{"Регион": "Север", "Товар": "А", "Месяц": "Янв", "Сумма": "100"},
		{"Регион": "Север", "Товар": "Б", "Месяц": "Янв", "Сумма": "30"},
		{"Регион": "Север", "Товар": "А", "Месяц": "Фев", "Сумма": "200"},
		{"Регион": "Юг", "Товар": "А", "Месяц": "Янв", "Сумма": "50"},
	}
	spec := report.Composition{
		Groupings: []string{"Регион", "Товар"},
		Columns:   []string{"Месяц"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
	}
	cr, err := ComposeCross(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cr.Rows) != 2 {
		t.Fatalf("ожидали 2 верхние группы (Север/Юг), получили %d", len(cr.Rows))
	}
	var sever, yug *CrossRow
	for _, r := range cr.Rows {
		switch r.Key {
		case "Север":
			sever = r
		case "Юг":
			yug = r
		}
	}
	if sever == nil || yug == nil {
		t.Fatal("не найдены группы Север/Юг")
		return
	}
	// Подытог Севера по Янв = 100+30, по Фев = 200.
	decEq(t, sever.Cells[colKey([]any{"Янв"}, 0)], "130")
	decEq(t, sever.Cells[colKey([]any{"Фев"}, 0)], "200")
	if len(sever.Children) != 2 {
		t.Fatalf("ожидали 2 дочерние строки у Севера, получили %d", len(sever.Children))
	}
	// У Юга нет февральских строк → ячейка Фев отсутствует.
	if _, ok := yug.Cells[colKey([]any{"Фев"}, 0)]; ok {
		t.Fatal("у Юга не должно быть ячейки за Фев")
	}
}

func TestCrossExprMeasure(t *testing.T) {
	// Вычисляемый показатель (Expr) считается и в ячейках кросс-таблицы — через
	// общий aggregate, как в обычном режиме.
	rows := []Row{
		{"Товар": "А", "Месяц": "Янв", "Сумма": "100"},
		{"Товар": "А", "Месяц": "Фев", "Сумма": "50"},
	}
	spec := report.Composition{
		Groupings: []string{"Товар"},
		Columns:   []string{"Месяц"},
		Measures: []report.Measure{
			{Field: "Сумма", Agg: "sum"},
			{Field: "Рент", Expr: "Сумма*2"},
		},
	}
	cr, err := ComposeCross(rows, spec, exprEval{})
	if err != nil {
		t.Fatal(err)
	}
	// 2 пути (месяца) × 2 показателя = 4 колонки.
	if len(cr.Cols) != 4 {
		t.Fatalf("ожидали 4 колонки (2 мес × 2 показателя), получили %d", len(cr.Cols))
	}
	rowA := cr.Rows[0]
	decEq(t, rowA.Cells[colKey([]any{"Янв"}, 0)], "100")
	decEq(t, rowA.Cells[colKey([]any{"Янв"}, 1)], "200") // 100*2
	decEq(t, rowA.Cells[colKey([]any{"Фев"}, 1)], "100") // 50*2
}

// TestCrossDistinctMeasureSameField: два показателя с одним Field, но разным Agg
// (sum/avg) дают РАЗНЫЕ колонки и не схлопываются (issue #17).
func TestCrossDistinctMeasureSameField(t *testing.T) {
	rows := []Row{
		{"Товар": "А", "Месяц": "Янв", "Сумма": "100"},
		{"Товар": "А", "Месяц": "Янв", "Сумма": "200"},
	}
	spec := report.Composition{
		Groupings: []string{"Товар"},
		Columns:   []string{"Месяц"},
		Measures: []report.Measure{
			{Field: "Сумма", Agg: "sum"}, // индекс 0
			{Field: "Сумма", Agg: "avg"}, // индекс 1 — тот же Field
		},
	}
	cr, err := ComposeCross(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	// 1 путь × 2 показателя = 2 колонки (раньше схлопывались в одну).
	if len(cr.Cols) != 2 {
		t.Fatalf("ожидали 2 колонки для sum/avg одного поля, получили %d", len(cr.Cols))
	}
	rowA := cr.Rows[0]
	decEq(t, rowA.Cells[colKey([]any{"Янв"}, 0)], "300") // sum = 100+200
	decEq(t, rowA.Cells[colKey([]any{"Янв"}, 1)], "150") // avg = (100+200)/2
}

// TestCrossWarnings: runtime-ошибка вычисляемого показателя в кросс-режиме
// попадает в CrossResult.Warnings, а не теряется молча (issue #26).
func TestCrossWarnings(t *testing.T) {
	rows := []Row{{"Товар": "А", "Месяц": "Янв", "Сумма": "10"}}
	spec := report.Composition{
		Groupings: []string{"Товар"},
		Columns:   []string{"Месяц"},
		Measures: []report.Measure{
			{Field: "Сумма", Agg: "sum"},
			{Field: "Вычисл", Expr: "что-то"},
		},
	}
	cr, err := ComposeCross(rows, spec, errEval{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cr.Warnings) == 0 {
		t.Fatal("ожидалось предупреждение об ошибке вычисляемого показателя в кросс-режиме")
	}
}

// TestCrossRowsSorted: spec.Sort применяется к строкам кросс-таблицы по ключу
// группировки (issue #27) — раньше строки шли строго в порядке выборки.
func TestCrossRowsSorted(t *testing.T) {
	rows := []Row{
		{"Товар": "В", "Месяц": "Янв", "Сумма": "30"},
		{"Товар": "А", "Месяц": "Янв", "Сумма": "10"},
		{"Товар": "Б", "Месяц": "Янв", "Сумма": "20"},
	}
	spec := report.Composition{
		Groupings: []string{"Товар"},
		Columns:   []string{"Месяц"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Sort:      []report.SortKey{{Field: "Товар", Dir: "asc"}},
	}
	cr, err := ComposeCross(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	want := []any{"А", "Б", "В"}
	for i, r := range cr.Rows {
		if r.Key != want[i] {
			t.Fatalf("строка %d: ожидали %v, получили %v (порядок %v)", i, want[i], r.Key,
				[]any{cr.Rows[0].Key, cr.Rows[1].Key, cr.Rows[2].Key})
		}
	}
}
