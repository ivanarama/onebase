package sheet

import (
	"bytes"
	"fmt"
	"github.com/xuri/excelize/v2"
	"strings"
	"testing"
)

func outputTestArea(rows, cols int, text string) *Area {
	a := NewArea(0, 0, rows-1, cols-1)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			a.Cells[fmt.Sprintf("%d,%d", r, c)] = NewCell(text)
		}
	}
	return a
}
func requireOutputCell(t *testing.T, d *Document, r, c int, text string) {
	t.Helper()
	v := d.GetCell(r, c)
	if v == nil || v.Text != text {
		t.Fatalf("cell R%dC%d =%v want %s", r+1, c+1, v, text)
	}
}
func TestTabularBlockOutput(t *testing.T) {
	t.Run("two_by_one", func(t *testing.T) {
		d := NewDocument()
		d.Put(outputTestArea(2, 1, "LEFT"))
		d.Append(outputTestArea(2, 1, "RIGHT"))
		requireOutputCell(t, d, 0, 1, "RIGHT")
		requireOutputCell(t, d, 1, 1, "RIGHT")
		r, c := d.ContentBounds()
		if r != 1 || c != 1 || d.CurrentRow != 2 {
			t.Fatal("horizontal block grew down instead of right")
		}
	})
	t.Run("repeated_append_then_put", func(t *testing.T) {
		d := NewDocument()
		d.Put(outputTestArea(2, 2, "LEFT"))
		d.Append(outputTestArea(2, 2, "RIGHT"))
		d.Append(outputTestArea(1, 1, "END"))
		d.Put(outputTestArea(1, 2, "NEXT"))
		requireOutputCell(t, d, 0, 4, "END")
		requireOutputCell(t, d, 2, 0, "NEXT")
		if d.GetCell(2, 2) != nil {
			t.Fatal("next Put inherited horizontal offset")
		}
	})
	t.Run("taller_right", func(t *testing.T) {
		d := NewDocument()
		d.Put(outputTestArea(2, 1, "LEFT"))
		d.Append(outputTestArea(4, 1, "RIGHT"))
		d.Put(outputTestArea(1, 1, "NEXT"))
		requireOutputCell(t, d, 3, 1, "RIGHT")
		requireOutputCell(t, d, 4, 0, "NEXT")
	})
	t.Run("shorter_right", func(t *testing.T) {
		d := NewDocument()
		d.Put(outputTestArea(4, 1, "LEFT"))
		d.Append(outputTestArea(1, 1, "RIGHT"))
		d.Put(outputTestArea(1, 1, "NEXT"))
		requireOutputCell(t, d, 0, 1, "RIGHT")
		requireOutputCell(t, d, 4, 0, "NEXT")
	})
	t.Run("declared_blank_tail", func(t *testing.T) {
		d := NewDocument()
		a := NewArea(0, 0, 1, 3)
		a.Cells["0,0"] = NewCell("LEFT")
		d.Put(a)
		d.Append(outputTestArea(1, 1, "RIGHT"))
		requireOutputCell(t, d, 0, 4, "RIGHT")
		if d.CurrentRow != 2 {
			t.Fatal("sparse rectangle height lost")
		}
	})
	t.Run("span_extent", func(t *testing.T) {
		d := NewDocument()
		a := outputTestArea(1, 1, "SPAN")
		a.Cells["0,0"].ColSpan = 3
		a.Cells["0,0"].RowSpan = 2
		d.Put(a)
		d.Append(outputTestArea(1, 1, "RIGHT"))
		d.Put(outputTestArea(1, 1, "NEXT"))
		requireOutputCell(t, d, 0, 3, "RIGHT")
		requireOutputCell(t, d, 2, 0, "NEXT")
	})
	t.Run("append_first", func(t *testing.T) {
		d := NewDocument()
		d.Append(outputTestArea(2, 1, "LEFT"))
		d.Append(outputTestArea(1, 1, "RIGHT"))
		d.Put(outputTestArea(1, 1, "NEXT"))
		requireOutputCell(t, d, 0, 0, "LEFT")
		requireOutputCell(t, d, 0, 1, "RIGHT")
		requireOutputCell(t, d, 2, 0, "NEXT")
	})
	t.Run("explicit_cells_before_append", func(t *testing.T) {
		d := NewDocument()
		d.SetCell(0, 0, "A")
		d.SetCell(0, 1, "B")
		d.Append(outputTestArea(1, 1, "RIGHT"))
		requireOutputCell(t, d, 0, 2, "RIGHT")
	})
	t.Run("clear_resets_block", func(t *testing.T) {
		d := NewDocument()
		d.Put(outputTestArea(3, 3, "OLD"))
		d.Append(outputTestArea(2, 3, "OLD"))
		d.Clear()
		d.Append(outputTestArea(1, 1, "FRESH"))
		d.Put(outputTestArea(1, 1, "NEXT"))
		requireOutputCell(t, d, 0, 0, "FRESH")
		requireOutputCell(t, d, 1, 0, "NEXT")
		if len(d.Cells) != 2 {
			t.Fatal("clear retained output state")
		}
	})
	t.Run("endpage_resets_block", func(t *testing.T) {
		d := NewDocument()
		d.Put(outputTestArea(2, 1, "OLD"))
		d.EndPage()
		r := d.CurrentRow
		d.Append(outputTestArea(1, 1, "PAGE"))
		requireOutputCell(t, d, r, 0, "PAGE")
	})
	t.Run("put_only", func(t *testing.T) {
		d := NewDocument()
		d.Put(outputTestArea(2, 2, "HEAD"))
		d.Put(outputTestArea(1, 1, "BODY"))
		d.Put(outputTestArea(1, 1, "FOOT"))
		requireOutputCell(t, d, 2, 0, "BODY")
		requireOutputCell(t, d, 3, 0, "FOOT")
	})
	t.Run("nil_and_empty_no_movement", func(t *testing.T) {
		d := NewDocument()
		d.Put(outputTestArea(2, 1, "LEFT"))
		d.Append(nil)
		d.Put(nil)
		d.Append(NewArea(0, 0, -1, 0))
		d.Append(outputTestArea(1, 1, "RIGHT"))
		requireOutputCell(t, d, 0, 1, "RIGHT")
	})
	t.Run("A002_header_month_footer", func(t *testing.T) {
		d := NewDocument()
		d.Put(outputTestArea(3, 12, "TITLE"))
		d.Put(outputTestArea(2, 3, "HEAD"))
		d.Append(outputTestArea(2, 9, "REASON"))
		d.Put(outputTestArea(1, 1, "NAME"))
		d.Append(outputTestArea(1, 11, "JAN"))
		d.Put(outputTestArea(1, 12, "FOOT"))
		requireOutputCell(t, d, 3, 3, "REASON")
		requireOutputCell(t, d, 4, 11, "REASON")
		requireOutputCell(t, d, 5, 1, "JAN")
		requireOutputCell(t, d, 6, 0, "FOOT")
		r, c := d.ContentBounds()
		if r != 6 || c != 11 {
			t.Fatalf("A002 drifted: %dx%d", r+1, c+1)
		}
	})
	t.Run("grouped_taller_header", func(t *testing.T) {
		d := NewDocument()
		d.Put(outputTestArea(1, 1, "HEAD"))
		d.Groups = []RowGroup{{Start: 0, HeaderEnd: 0, Level: 1, Expanded: true}}
		d.Append(outputTestArea(3, 1, "RIGHT"))
		if d.Groups[0].HeaderEnd != 2 {
			t.Fatal("horizontal header height not reflected in group")
		}
	})
	t.Run("draw_does_not_extend_previous_group_header", func(t *testing.T) {
		d := NewDocument()
		d.Put(outputTestArea(1, 1, "HEAD"))
		d.Groups = []RowGroup{{Start: 0, HeaderEnd: 0, Level: 1, Expanded: true}}
		d.Draw("own-placeholder")
		d.Append(outputTestArea(3, 1, "NEWBLOCK"))
		if d.Groups[0].HeaderEnd != 0 {
			t.Fatal("new block extended previous grouped header")
		}
		requireOutputCell(t, d, 1, 1, "NEWBLOCK")
	})
	t.Run("copy_values_styles_and_export_coordinates", func(t *testing.T) {
		d := NewDocument()
		left := outputTestArea(2, 1, "LEFT")
		right := outputTestArea(2, 1, "RIGHT")
		right.Cells["0,0"].Value = 12.5
		right.Cells["0,0"].Format = "0.00"
		right.Cells["0,0"].Bold = true
		right.Cells["1,0"].Height = 40
		d.Put(left)
		d.Append(right)
		d.Put(outputTestArea(1, 1, "NEXT"))
		cell := d.GetCell(0, 1)
		if cell.Value != 12.5 || cell.Format != "0.00" || !cell.Bold || d.GetCell(1, 1).Height != 40 {
			t.Fatal("typed value/style lost")
		}
		cell.Text = "DEST"
		if right.Cells["0,0"].Text != "RIGHT" {
			t.Fatal("destination overwrote source cell")
		}
		h := d.HTMLString()
		if !strings.Contains(h, "12.50") || !strings.Contains(h, "NEXT") {
			t.Fatal("HTML missing copied cells")
		}
		data, e := d.XLSX()
		if e != nil {
			t.Fatal(e)
		}
		f, e := excelize.OpenReader(bytes.NewReader(data))
		if e != nil {
			t.Fatal(e)
		}
		defer f.Close()
		sheet := f.GetSheetList()[0]
		for ref, want := range map[string]string{"A1": "LEFT", "B1": "12.50", "B2": "RIGHT", "A3": "NEXT"} {
			got, e := f.GetCellValue(sheet, ref)
			if e != nil || got != want {
				t.Fatalf("XLSX %s=%q want%q", ref, got, want)
			}
		}
		pdf, e := d.PDF(PDFOptions{})
		if e != nil || !bytes.HasPrefix(pdf, []byte("%PDF-")) {
			t.Fatal("PDF export failed")
		}
	})
}
