package sheet

import (
	"bytes"
	"compress/zlib"
	"io"
	"regexp"
	"testing"
)

// pdfTextLines считает строки текста, выведенные на страницы PDF: каждая
// строка ячейки — один оператор Tj в (сжатых) потоках содержимого.
func pdfTextLines(t *testing.T, data []byte) int {
	t.Helper()
	streams := regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`).FindAllSubmatch(data, -1)
	n := 0
	for _, m := range streams {
		r, err := zlib.NewReader(bytes.NewReader(m[1]))
		if err != nil {
			continue // не FlateDecode (шрифты и т.п. — проверяем только тексты)
		}
		plain, err := io.ReadAll(r)
		if err != nil {
			continue
		}
		n += len(regexp.MustCompile(`\)\s*Tj|>\s*Tj`).FindAll(plain, -1))
	}
	return n
}

// Узкая колонка: текст, который помещается в ширину ячейки за вычетом её
// полей (cellPadMM), выводится одной строкой. Раньше поле fpdf (cMargin, 1 мм)
// вычиталось ещё раз, и «ОТ» шириной 2,4 мм рвалось на «О» / «Т» в колонке с
// 4,35 мм места — так печатался табель мастера с колонкой на каждый день.
func TestPDFNarrowCellFitsTextWithoutSecondMargin(t *testing.T) {
	cases := []struct {
		text  string
		lines int
	}{
		{"ОТ", 1},   // 2,4 мм при 5 pt
		{"8 22", 1}, // 3,25 мм
		{"1456", 1}, // 3,76 мм
		// Не помещается и после исправления — перенос по-прежнему работает.
		{"8 22 8 22", 2},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			d := NewDocument()
			d.SetColumnWidth(1, 24) // 6,35 мм: 4,35 мм на текст
			c := d.GetOrCreateCell(0, 0)
			c.Text = tc.text
			c.FontSize = 5
			data, err := d.PDF(PDFOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if got := pdfTextLines(t, data); got != tc.lines {
				t.Fatalf("%q в колонке 6,35 мм: строк %d, want %d", tc.text, got, tc.lines)
			}
		})
	}
}
