package pdfimport

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/printform"
	"github.com/ivantit66/onebase/internal/sheet"
)

// buildSyntheticTablePDF строит PDF с кириллической таблицей: шапка со спаном,
// строка-заголовок колонок, строки данных. Границы — per-side, чтобы извлекатель
// линий восстановил сетку. Возвращает байты PDF.
func buildSyntheticTablePDF(t *testing.T) []byte {
	t.Helper()
	d := sheet.NewDocument()

	allBorders := func(c *sheet.Cell) {
		c.BorderTop, c.BorderBottom, c.BorderLeft, c.BorderRight = "thin", "thin", "thin", "thin"
	}

	// Колонки фиксированной ширины (px).
	d.SetColumnWidth(1, 200)
	d.SetColumnWidth(2, 120)
	d.SetColumnWidth(3, 120)

	// Строка 0: заголовок со спаном на 3 колонки.
	title := d.GetOrCreateCell(0, 0)
	title.Text = "Накладная № 42"
	title.Bold = true
	title.FontSize = 12
	title.Align = "center"
	title.ColSpan = 3
	allBorders(title)

	// Строка 1: заголовки колонок.
	headers := []string{"Наименование", "Количество", "Сумма"}
	for c, h := range headers {
		cell := d.GetOrCreateCell(1, c)
		cell.Text = h
		cell.Bold = true
		cell.Align = "center"
		allBorders(cell)
	}

	// Строки данных.
	data := [][]string{
		{"Стол письменный дубовый", "2", "15000,00"},
		{"Стул офисный", "10", "30000,00"},
		{"Лампа настольная", "5", "7500,00"},
	}
	for r, rowData := range data {
		for c, val := range rowData {
			cell := d.GetOrCreateCell(2+r, c)
			cell.Text = val
			if c == 0 {
				cell.Align = "left"
			} else {
				cell.Align = "right"
			}
			allBorders(cell)
		}
	}

	b, err := d.PDF(sheet.PDFOptions{Title: "Синтетическая накладная"})
	if err != nil {
		t.Fatalf("sheet.PDF: %v", err)
	}
	return b
}

func TestImportRoundtripCyrillic(t *testing.T) {
	pdfBytes := buildSyntheticTablePDF(t)

	tpl, err := ImportBytes(pdfBytes, 1)
	if err != nil {
		t.Fatalf("ImportBytes: %v", err)
	}
	if tpl == nil {
		t.Fatal("nil template")
		return
	}
	if len(tpl.Areas) != 1 {
		t.Fatalf("ожидалась 1 область, получено %d", len(tpl.Areas))
	}
	area := tpl.Areas[0]
	if area.Name != "Страница1" {
		t.Errorf("имя области = %q, ожидалось Страница1", area.Name)
	}

	// Весь текст макета.
	all := layoutText(tpl)

	// Кириллица не мусор: ключевые тексты на месте.
	wantTexts := []string{
		"Накладная", "Наименование", "Количество", "Сумма",
		"Стол", "Стул", "Лампа",
	}
	for _, w := range wantTexts {
		if !strings.Contains(all, w) {
			t.Errorf("текст %q не найден в импортированном макете.\nВесь текст:\n%s", w, all)
		}
	}

	// Никакого «мусора» (replacement char).
	if strings.ContainsRune(all, '�') {
		t.Errorf("в извлечённом тексте есть символ-замена (мусор кириллицы)")
	}

	// Сетка близка: ожидаем 3 колонки.
	if len(tpl.Columns) < 3 {
		t.Errorf("ожидалось >=3 колонки, получено %d", len(tpl.Columns))
	}
}

func TestImportRoundtripSpans(t *testing.T) {
	pdfBytes := buildSyntheticTablePDF(t)
	tpl, err := ImportBytes(pdfBytes, 1)
	if err != nil {
		t.Fatalf("ImportBytes: %v", err)
	}

	// Заголовок «Накладная № 42» должен иметь colspan 3 (объединён на всю ширину).
	found := false
	for _, area := range tpl.Areas {
		for _, row := range area.Rows {
			for _, cell := range row.Cells {
				if strings.Contains(cell.Text, "Накладная") {
					found = true
					if cell.ColSpan < 2 {
						t.Errorf("ячейка «Накладная» colspan=%d, ожидался спан (>=2)", cell.ColSpan)
					}
				}
			}
		}
	}
	if !found {
		t.Error("ячейка «Накладная» не найдена")
	}
}

func TestImportBordersDetected(t *testing.T) {
	pdfBytes := buildSyntheticTablePDF(t)
	tpl, err := ImportBytes(pdfBytes, 1)
	if err != nil {
		t.Fatalf("ImportBytes: %v", err)
	}
	// Хотя бы у одной ячейки есть границы по сторонам.
	withBorders := 0
	for _, area := range tpl.Areas {
		for _, row := range area.Rows {
			for _, cell := range row.Cells {
				if !cell.Borders.IsZero() {
					withBorders++
				}
			}
		}
	}
	if withBorders == 0 {
		t.Error("ни у одной ячейки не определены границы по сторонам — сетка не восстановлена")
	}
}

// layoutText собирает весь текст макета (для проверок).
func layoutText(tpl *printform.LayoutTemplate) string {
	var sb strings.Builder
	for _, area := range tpl.Areas {
		for _, row := range area.Rows {
			for _, cell := range row.Cells {
				sb.WriteString(cell.Text)
				sb.WriteString(" | ")
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}
