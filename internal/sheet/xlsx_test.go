package sheet

import (
	"bytes"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"
)

// Табличный документ «как макет 1С» → xlsx: открываем книгу библиотекой и
// сверяем значения, шрифт, рамки, заливку, выравнивание, перенос,
// объединение, ширины и высоты.
func TestDocumentXLSXKeepsLayoutStyles(t *testing.T) {
	d := NewDocument()
	head := d.GetOrCreateCell(0, 0)
	head.Text, head.Value = "Заявки бригаде", "Заявки бригаде"
	head.FontFamily, head.FontSize, head.Bold = "Calibri", 11, true
	head.Align, head.Vertical = "center", "center"
	head.BackColor = "#DDEBF7"
	head.BorderLeft, head.BorderTop, head.BorderRight, head.BorderBottom = "thin", "thin", "thin", "medium"
	d.Merge(1, 1, 1, 3)

	num := d.GetOrCreateCell(1, 0)
	num.Text, num.Value = "1 250,50", decimal.RequireFromString("1250.5")
	num.FontFamily, num.FontSize, num.Italic, num.Align = "Calibri", 11, true, "right"
	date := d.GetOrCreateCell(1, 1)
	date.Text, date.Value = "21.10.2026", time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	date.FontFamily, date.FontSize = "Calibri", 11
	code := d.GetOrCreateCell(1, 2)
	code.Text, code.Value = "007", "007"
	code.FontFamily, code.FontSize, code.TextColor = "Calibri", 11, "red"
	d.SetColumnWidth(1, 145)
	d.SetColumnWidth(3, 75)
	d.SetRowHeight(1, 40)

	data, err := d.XLSX()
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("книга не открывается: %v", err)
	}
	defer f.Close() //nolint:errcheck
	const sh = "Лист1"

	if v, _ := f.GetCellValue(sh, "A1"); v != "Заявки бригаде" {
		t.Fatalf("A1 = %q", v)
	}
	merges, _ := f.GetMergeCells(sh)
	if len(merges) != 1 || merges[0].GetStartAxis() != "A1" || merges[0].GetEndAxis() != "C1" {
		t.Fatalf("объединение шапки: %+v", merges)
	}
	style := func(ref string) *excelize.Style {
		t.Helper()
		id, err := f.GetCellStyle(sh, ref)
		if err != nil {
			t.Fatal(err)
		}
		st, err := f.GetStyle(id)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	h := style("A1")
	if h.Font == nil || h.Font.Family != "Calibri" || h.Font.Size != 11 || !h.Font.Bold {
		t.Errorf("шрифт шапки: %+v", h.Font)
	}
	if h.Alignment == nil || h.Alignment.Horizontal != "center" || h.Alignment.Vertical != "center" || !h.Alignment.WrapText {
		t.Errorf("выравнивание/перенос шапки: %+v", h.Alignment)
	}
	if len(h.Fill.Color) == 0 || h.Fill.Color[0] != "DDEBF7" {
		t.Errorf("заливка шапки: %+v", h.Fill)
	}
	borders := map[string]int{}
	for _, b := range h.Border {
		borders[b.Type] = b.Style
	}
	if borders["left"] != 1 || borders["top"] != 1 || borders["right"] != 1 || borders["bottom"] != 2 {
		t.Errorf("рамки шапки: %+v", h.Border)
	}
	if c := style("C1"); len(c.Border) == 0 {
		t.Error("у объединённой области рамка не продолжена на правую ячейку")
	}

	if typ, _ := f.GetCellType(sh, "A2"); typ != excelize.CellTypeNumber && typ != excelize.CellTypeUnset {
		t.Errorf("число записано не числом: тип %v", typ)
	}
	if raw, _ := f.GetCellValue(sh, "A2", excelize.Options{RawCellValue: true}); raw != "1250.5" {
		t.Errorf("число A2 = %q, ожидалось 1250.5", raw)
	}
	if n := style("A2"); n.Font == nil || !n.Font.Italic || n.Alignment == nil || n.Alignment.Horizontal != "right" {
		t.Errorf("курсив/выравнивание числа: %+v %+v", n.Font, n.Alignment)
	}
	if raw, _ := f.GetCellValue(sh, "B2", excelize.Options{RawCellValue: true}); raw != "46316" {
		t.Errorf("дата записана не датой Excel: %q", raw)
	}
	if st := style("B2"); st.NumFmt != 14 {
		t.Errorf("формат даты: %d", st.NumFmt)
	}
	if v, _ := f.GetCellValue(sh, "C2"); v != "007" {
		t.Errorf("код потерял ведущие нули: %q", v)
	}
	if c := style("C2"); c.Font == nil || c.Font.Color != "FF0000" {
		t.Errorf("цвет текста: %+v", c.Font)
	}
	if w, _ := f.GetColWidth(sh, "A"); w < 19 || w > 21 {
		t.Errorf("ширина колонки A = %v, ожидалось ≈20 символов (145 px)", w)
	}
	if h, _ := f.GetRowHeight(sh, 1); h != 30 {
		t.Errorf("высота строки 1 = %v pt, ожидалось 30 (40 px)", h)
	}
}

func TestXLSXColor(t *testing.T) {
	for in, want := range map[string]string{"#abc": "AABBCC", "#DDEBF7": "DDEBF7", "rgb(255, 0, 16)": "FF0010", "Red": "FF0000", "url(x)": "", "": ""} {
		if got := xlsxColor(in); got != want {
			t.Errorf("xlsxColor(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}
