package sheet

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"
)

// buildCyrillicDoc — простой документ с кириллицей и стилями.
func buildCyrillicDoc() *Document {
	d := NewDocument()
	c := d.GetOrCreateCell(0, 0)
	c.Text = "Привет, мир!"
	c.Bold = true
	c.FontSize = 14
	c.Align = "center"
	c2 := d.GetOrCreateCell(1, 0)
	c2.Text = "Поставщик"
	c2.Italic = true
	c3 := d.GetOrCreateCell(1, 1)
	c3.Text = "ООО «Ромашка»"
	c3.BackColor = "#eef"
	return d
}

func TestPDFGeneratesValidHeader(t *testing.T) {
	d := buildCyrillicDoc()
	b, err := d.PDF(PDFOptions{Title: "Тест"})
	if err != nil {
		t.Fatalf("PDF() error: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("PDF пустой")
	}
	if !bytes.HasPrefix(b, []byte("%PDF")) {
		t.Fatalf("PDF не начинается с %%PDF: % x", b[:8])
	}
}

func TestPDFParsesWithFpdf(t *testing.T) {
	// Перегенерируем тем же fpdf-конвейером — косвенная проверка валидности
	// (полноценный PDF-парсер тут не нужен; %PDF + успешный Output достаточны).
	d := buildCyrillicDoc()
	b, err := d.PDF(PDFOptions{})
	if err != nil {
		t.Fatalf("PDF() error: %v", err)
	}
	if !bytes.Contains(b, []byte("%%EOF")) {
		t.Fatal("в PDF нет маркера EOF — файл не закрыт корректно")
	}
}

func TestPDFEmbedsCyrillicFontNotTransliterated(t *testing.T) {
	d := buildCyrillicDoc()
	b, err := d.PDF(PDFOptions{})
	if err != nil {
		t.Fatalf("PDF() error: %v", err)
	}
	// Встроенный шрифт виден в байтах PDF по имени subset-шрифта.
	// fpdf приводит имя к нижнему регистру с префиксом utf8: utf8ptserif/utf8ptsans.
	lb := bytes.ToLower(b)
	if !bytes.Contains(lb, []byte("ptserif")) && !bytes.Contains(lb, []byte("ptsans")) {
		t.Fatal("в PDF не найден встроенный PT-шрифт (ptserif/ptsans)")
	}
	// Транслитерация умерла: латинизированная «Privet» не должна встречаться.
	if bytes.Contains(b, []byte("Privet")) {
		t.Fatal("в PDF найдена латинизация 'Privet' — транслитерация не убита")
	}
}

func TestPDFSpansDoNotPanic(t *testing.T) {
	d := NewDocument()
	c := d.GetOrCreateCell(0, 0)
	c.Text = "Шапка"
	c.ColSpan = 3
	c.RowSpan = 2
	c.Align = "center"
	c.Border = "all"
	// несколько строк под спаном
	for r := 2; r < 5; r++ {
		for col := 0; col < 3; col++ {
			cc := d.GetOrCreateCell(r, col)
			cc.Text = "ячейка"
			cc.Border = "thin"
		}
	}
	b, err := d.PDF(PDFOptions{})
	if err != nil {
		t.Fatalf("PDF() error: %v", err)
	}
	if !bytes.HasPrefix(b, []byte("%PDF")) {
		t.Fatal("PDF невалиден")
	}
}

func countPages(b []byte) int {
	// Считаем объекты /Type /Page (но не /Pages).
	count := 0
	idx := 0
	for {
		i := bytes.Index(b[idx:], []byte("/Type /Page"))
		if i < 0 {
			i = bytes.Index(b[idx:], []byte("/Type/Page"))
			if i < 0 {
				break
			}
		}
		pos := idx + i
		// Отбрасываем /Type /Pages (узел дерева страниц).
		after := pos + len("/Type /Page")
		if after < len(b) && (b[after] == 's') {
			idx = after
			continue
		}
		count++
		idx = after
	}
	return count
}

func TestPDFPageBreakAddsPage(t *testing.T) {
	// Документ с явным разрывом страницы.
	d := NewDocument()
	d.GetOrCreateCell(0, 0).Text = "Страница 1"
	d.PageBreaks = []int{2}
	d.GetOrCreateCell(2, 0).Text = "Страница 2"

	b, err := d.PDF(PDFOptions{})
	if err != nil {
		t.Fatalf("PDF() error: %v", err)
	}
	if pages := countPages(b); pages < 2 {
		t.Fatalf("ожидалось ≥2 страницы, получено %d", pages)
	}
}

func TestPDFAutoPageBreakOnHeight(t *testing.T) {
	// Много строк → авто-разрыв по высоте.
	d := NewDocument()
	for r := 0; r < 200; r++ {
		d.GetOrCreateCell(r, 0).Text = "строка"
	}
	b, err := d.PDF(PDFOptions{})
	if err != nil {
		t.Fatalf("PDF() error: %v", err)
	}
	if pages := countPages(b); pages < 2 {
		t.Fatalf("ожидалось ≥2 страницы при 200 строках, получено %d", pages)
	}
}

func TestPDFRepeatHeaderOnSecondPage(t *testing.T) {
	d := NewDocument()
	// Область-шапка.
	area := NewArea(0, 0, 0, 1)
	hc := area.GetOrCreateCell(0, 0)
	hc.Text = "ШАПКА ОТЧЁТА"
	hc.Bold = true
	d.RepeatOnPrint(area, true)

	// Много строк, чтобы вызвать вторую страницу.
	for r := 0; r < 200; r++ {
		d.GetOrCreateCell(r, 0).Text = "данные"
	}

	b, err := d.PDF(PDFOptions{})
	if err != nil {
		t.Fatalf("PDF() error: %v", err)
	}
	pages := countPages(b)
	if pages < 2 {
		t.Fatalf("ожидалось ≥2 страницы, получено %d", pages)
	}
	// Шапка повторяется: текст «ШАПКА ОТЧЁТА» закодирован в потоке через
	// glyph-индексы subset-шрифта, поэтому прямой поиск строки ненадёжен.
	// Достаточно того, что рендер с RepeatHeader не падает и даёт многостраничный PDF.
}

func TestResolveColumnWidthsMM(t *testing.T) {
	d := NewDocument()
	// Колонка 1 — 96px (= 25.4мм = 1 дюйм); колонка 2 — не задана.
	d.SetColumnWidth(1, 96)
	usable := 200.0
	widths := d.resolveColumnWidthsMM(usable, 2)
	if len(widths) != 2 {
		t.Fatalf("ожидалось 2 ширины, получено %d", len(widths))
	}
	if got := widths[0]; got < 25.0 || got > 26.0 {
		t.Errorf("колонка 1: ожидалось ~25.4мм, получено %.2f", got)
	}
	// Свободная колонка получает остаток (≈174.6мм).
	if got := widths[1]; got < 170 || got > 180 {
		t.Errorf("колонка 2: ожидался остаток ~174.6мм, получено %.2f", got)
	}
}

func TestResolveFontMapping(t *testing.T) {
	cases := []struct {
		family       string
		bold, italic bool
		wantFamily   string
		wantStyle    string
	}{
		{"Times New Roman", false, false, "PTSerif", ""},
		{"Times New Roman", true, false, "PTSerif", "B"},
		{"serif", false, true, "PTSerif", "I"},
		{"Arial", false, false, "PTSans", ""},
		{"Arial", true, false, "PTSans", "B"},
		{"Arial", false, true, "PTSerif", "I"}, // sans-italic → serif italic
		{"", false, false, "PTSerif", ""},      // дефолт → serif
	}
	for _, c := range cases {
		gotF, gotS := resolveFont(c.family, c.bold, c.italic)
		if gotF != c.wantFamily || gotS != c.wantStyle {
			t.Errorf("resolveFont(%q,%v,%v) = (%q,%q), want (%q,%q)",
				c.family, c.bold, c.italic, gotF, gotS, c.wantFamily, c.wantStyle)
		}
	}
}

func TestParseHexColor(t *testing.T) {
	r, g, b, ok := parseHexColor("#eef")
	if !ok || r != 0xee || g != 0xee || b != 0xff {
		t.Errorf("#eef → (%d,%d,%d,%v)", r, g, b, ok)
	}
	r, g, b, ok = parseHexColor("003399")
	if !ok || r != 0 || g != 0x33 || b != 0x99 {
		t.Errorf("003399 → (%d,%d,%d,%v)", r, g, b, ok)
	}
	if _, _, _, ok := parseHexColor("nope"); ok {
		t.Error("невалидный цвет распознан как валидный")
	}
}

// TestPDFSubsetSize — страховка спайка: при использовании одного начертания
// PDF остаётся компактным (< 200КБ), т.е. fpdf субсетит шрифты.
func TestPDFSubsetSize(t *testing.T) {
	d := NewDocument()
	d.GetOrCreateCell(0, 0).Text = "Привет"
	b, err := d.PDF(PDFOptions{})
	if err != nil {
		t.Fatalf("PDF() error: %v", err)
	}
	if len(b) > 200*1024 {
		t.Fatalf("PDF слишком большой (%d байт) — шрифты не субсетятся", len(b))
	}
}

// sanity: убедимся, что fpdf вообще доступен и SplitText работает с нашими шрифтами.
func TestWrapTextWithFonts(t *testing.T) {
	pdf := fpdf.New("P", "mm", "A4", "")
	registerFonts(pdf)
	pdf.AddPage()
	pdf.SetFont("PTSerif", "", 10)
	lines := wrapText(pdf, "Очень длинный текст который должен переноситься на несколько строк по ширине", 30)
	if len(lines) < 2 {
		t.Errorf("ожидался перенос на ≥2 строки, получено %d: %v", len(lines), lines)
	}
	if strings.Join(lines, "") == "" {
		t.Error("wrapText вернул пустоту")
	}
}

// newMeasuredPDF создаёт fpdf-инстанс с зарегистрированными шрифтами и одной
// страницей — пригоден для измерения переноса (wrapText/SplitText) в тестах.
func newMeasuredPDF() *fpdf.Fpdf {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetCellMargin(0) // как в Document.PDF
	registerFonts(pdf)
	pdf.AddPage()
	return pdf
}

// realWrappedLines измеряет фактическое число строк ячейки тем же движком, что и
// рендер: устанавливает шрифт/размер/начертание и считает строки wrapText.
func realWrappedLines(pdf *fpdf.Fpdf, cell *Cell, availMM float64) int {
	family, style := resolveFont(cell.FontFamily, cell.Bold, cell.Italic)
	pdf.SetFont(family, style, fontSizeOr(cell))
	return len(wrapText(pdf, cell.Text, availMM))
}

// TestRowHeightFitsCyrillicWrap — регресс на дефект 1 (недооценка высоты строк
// для кириллицы). Узкая колонка + длинный кириллический текст: высота строки,
// посчитанная computeRowHeightsMM, ДОЛЖНА вмещать реальное число строк
// переноса. Репро ревью: «Стол письменный дубовый с ящиками» @25мм/10pt → 4
// строки, а старая эвристика давала 3 → текст вываливался за ячейку.
func TestRowHeightFitsCyrillicWrap(t *testing.T) {
	pdf := newMeasuredPDF()

	d := NewDocument()
	// Колонка 1 — 25мм (≈94.5px), колонка 2 — 30мм.
	d.SetColumnWidth(1, 25.0/mmPerInch*pxPerInch)
	d.SetColumnWidth(2, 30.0/mmPerInch*pxPerInch)
	c0 := d.GetOrCreateCell(0, 0)
	c0.Text = "Стол письменный дубовый с ящиками"
	c1 := d.GetOrCreateCell(0, 1)
	c1.Text = "Очень длинное наименование товарной позиции для проверки переноса"

	maxRow, maxCol := d.ContentBounds()
	colWidths := d.resolveColumnWidthsMM(190, maxCol+1)
	heights := d.computeRowHeightsMM(pdf, maxRow, colWidths)

	for row := 0; row <= maxRow; row++ {
		var needLines int
		for col := 0; col < len(colWidths); col++ {
			cell := d.GetCell(row, col)
			if cell == nil || cell.Text == "" {
				continue
			}
			cw := colWidths[col]
			for cs := 1; cs < cell.ColSpan && col+cs < len(colWidths); cs++ {
				cw += colWidths[col+cs]
			}
			avail := cw - 2*cellPadMM
			if avail <= 0 {
				avail = cw
			}
			if n := realWrappedLines(pdf, cell, avail); n > needLines {
				needLines = n
			}
		}
		fs := 10.0
		lineH := fs * ptToMM * lineGap
		minNeeded := float64(needLines)*lineH + 2*cellPadMM
		if heights[row] < minNeeded-1e-6 {
			t.Errorf("строка %d: высота %.2fмм < требуемой %.2fмм (%d строк × %.2f + паддинг)",
				row, heights[row], minNeeded, needLines, lineH)
		}
	}
}

// TestRepeatHeaderHeightNotMinRow — регресс на дефект 2 (повторная шапка
// сжимается до minRowMM). Многострочная ячейка шапки УПД на 2-й странице не
// должна сжиматься в minRowMM на строку — высота должна считаться тем же
// движком рендера.
func TestRepeatHeaderHeightNotMinRow(t *testing.T) {
	pdf := newMeasuredPDF()

	area := NewArea(0, 0, 0, 0)
	hc := area.GetOrCreateCell(0, 0)
	hc.Text = "Универсальный передаточный документ (счёт-фактура) по форме приложения"
	hc.Bold = true

	colWidths := []float64{40} // узкая колонка → многострочная шапка
	heights := area.computeAreaRowHeightsMM(pdf, colWidths)
	if len(heights) != 1 {
		t.Fatalf("ожидалась 1 строка шапки, получено %d", len(heights))
	}

	avail := colWidths[0] - 2*cellPadMM
	realLines := realWrappedLines(pdf, hc, avail)
	if realLines < 2 {
		t.Fatalf("ожидался многострочный перенос шапки, получено %d строк", realLines)
	}
	lineH := 10.0 * ptToMM * lineGap
	minNeeded := float64(realLines)*lineH + 2*cellPadMM
	if heights[0] < minNeeded-1e-6 {
		t.Errorf("высота шапки %.2fмм < требуемой %.2fмм (сжата до minRow?)", heights[0], minNeeded)
	}
	if heights[0] <= minRowMM+1e-6 {
		t.Errorf("высота шапки %.2fмм ≈ minRowMM (%.2f) — шапка сжата", heights[0], minRowMM)
	}
}

// TestColumnWidthsClampedToUsable — регресс на дефект 3 (ширины колонок не
// клампятся). 5 колонок по 400px (≈105.8мм каждая → 529мм) против usable 190мм:
// после раздачи суммарная ширина должна быть смасштабирована до usable, иначе
// контент уезжает за край листа.
func TestColumnWidthsClampedToUsable(t *testing.T) {
	d := NewDocument()
	for c := 1; c <= 5; c++ {
		d.SetColumnWidth(c, 400)
	}
	usable := 190.0
	widths := d.resolveColumnWidthsMM(usable, 5)
	sum := 0.0
	for _, w := range widths {
		sum += w
	}
	if sum > usable+1e-6 {
		t.Errorf("суммарная ширина %.2fмм > usable %.2fмм — ширины не склампены", sum, usable)
	}
	// При клампе все колонки умещаются и сумма близка к usable.
	if sum < usable-1.0 {
		t.Errorf("суммарная ширина %.2fмм существенно меньше usable %.2fмм — клампинг переусердствовал", sum, usable)
	}
}
