package sheet

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestTabularAutoScaleWideKPI(t *testing.T) {
	for _, cols := range []int{63, 67} {
		t.Run(fmt.Sprint(cols), func(t *testing.T) {
			d := NewDocument()
			d.AutoScale = true
			d.Page.Orientation = "landscape"
			for c := 1; c <= cols; c++ {
				d.SetColumnWidth(c, 80)
			}
			for r := 0; r < 51; r++ {
				for c := 0; c < cols; c++ {
					cell := d.GetOrCreateCell(r, c)
					cell.Text = "Сумма KPI"
					cell.FontSize = 10
					cell.Border = "thin"
				}
			}
			h := NewArea(0, 0, 19, cols-1)
			for r := 0; r < 20; r++ {
				for c := 0; c < cols; c++ {
					v := *d.GetCell(r, c)
					h.Cells[fmt.Sprintf("%d,%d", r, c)] = &v
				}
			}
			d.RepeatHeader = true
			d.HeaderArea = h
			originalHTML := d.HTMLString()
			pdf, err := d.PDF(PDFOptions{})
			if err != nil {
				t.Fatal(err)
			}
			pages := regexp.MustCompile(`/Type\s*/Page\b`).FindAll(pdf, -1)
			if len(pages) != 1 {
				t.Fatalf("uniform scaled synthetic 51x%d pages=%d, want1", cols, len(pages))
			}
			streams := payloadPDFStreams(t, pdf)
			if !strings.Contains(streams, " cm") {
				t.Fatal("uniform transform missing")
			}
			if d.GetCell(0, 0).FontSize != 10 || d.ColWidths[1] != 80 || d.HTMLString() != originalHTML {
				t.Fatal("printing mutated screen layout")
			}
			data, err := d.XLSX()
			if err != nil {
				t.Fatal(err)
			}
			book, err := excelize.OpenReader(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := book.Close(); err != nil {
					t.Error(err)
				}
			}()
			layout, err := book.GetPageLayout("Лист1")
			if err != nil {
				t.Fatal(err)
			}
			props, err := book.GetSheetProps("Лист1")
			if err != nil {
				t.Fatal(err)
			}
			if layout.FitToWidth == nil || *layout.FitToWidth != 1 || layout.FitToHeight == nil || *layout.FitToHeight != 0 || props.FitToPage == nil || !*props.FitToPage {
				t.Fatal("XLSX width-fit setup missing")
			}
		})
	}
}

func TestTabularAutoScaleKeepsExplicitBreaksAndRepeatHeader(t *testing.T) {
	d := NewDocument()
	d.AutoScale = true
	d.Page.Orientation = "landscape"
	for c := 1; c <= 67; c++ {
		d.SetColumnWidth(c, 80)
	}
	header := NewArea(0, 0, 0, 66)
	header.Cells["0,0"] = NewCell("Repeat own header")
	d.Put(header)
	d.RepeatHeader = true
	d.HeaderArea = header
	d.GetOrCreateCell(1, 66).Text = "first"
	d.GetOrCreateCell(2, 66).Text = "second"
	d.PageBreaks = []int{2}
	pdf, err := d.PDF(PDFOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(regexp.MustCompile(`/Type\s*/Page\b`).FindAll(pdf, -1)); n != 2 {
		t.Fatalf("explicit break changed: pages=%d", n)
	}
	if n := strings.Count(payloadPDFStreams(t, pdf), " cm"); n < 2 {
		t.Fatal("print transformation missing after page break")
	}
}
