package sheet

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestTabularExportXLSXViewAndFormats(t *testing.T) {
	d := NewDocument()
	for r := 0; r < 7; r++ {
		d.GetOrCreateCell(r, 0).Text = "group or detail"
	}
	d.Groups = []RowGroup{{Start: 0, HeaderEnd: 0, Level: 1, Expanded: true}, {Start: 1, HeaderEnd: 1, Level: 2, Expanded: false}, {Start: 4, HeaderEnd: 4, Level: 2, Expanded: true}}
	d.FreezeRows, d.FreezeCols, d.HideGrid = 2, 1, true
	d.Page.Orientation = "landscape"
	d.RepeatHeader = true
	d.HeaderArea = &Area{Cells: map[string]*Cell{"0,0": NewCell("header")}}
	a, b := d.GetOrCreateCell(2, 0), d.GetOrCreateCell(3, 0)
	a.Value, a.Text, a.Format = 12.5, "raw a", "0.0000"
	b.Value, b.Text, b.Format = 0.125, "raw b", "0.0%"
	data, err := d.XLSX()
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	}()
	sh := f.GetSheetName(0)
	for row, want := range map[int]uint8{1: 0, 2: 1, 3: 2, 4: 2, 5: 1, 6: 2, 7: 2} {
		level, err := f.GetRowOutlineLevel(sh, row)
		if err != nil || level != want {
			t.Fatalf("outline row%d=%d %v, want%d", row, level, err, want)
		}
	}
	for row, want := range map[int]bool{1: true, 2: true, 3: false, 4: false, 5: true, 6: true, 7: true} {
		visible, err := f.GetRowVisible(sh, row)
		if err != nil || visible != want {
			t.Fatalf("visible row%d=%v %v", row, visible, err)
		}
	}
	panes, err := f.GetPanes(sh)
	if err != nil || !panes.Freeze || panes.XSplit != 1 || panes.YSplit != 2 || panes.TopLeftCell != "B3" {
		t.Fatalf("panes=%+v %v", panes, err)
	}
	view, err := f.GetSheetView(sh, 0)
	if err != nil || view.ShowGridLines == nil || *view.ShowGridLines {
		t.Fatalf("grid=%+v %v", view, err)
	}
	page, err := f.GetPageLayout(sh)
	if err != nil || page.Orientation == nil || *page.Orientation != "landscape" {
		t.Fatalf("page=%+v %v", page, err)
	}
	found := false
	for _, name := range f.GetDefinedName() {
		if strings.HasSuffix(name.Name, "Print_Titles") && strings.Contains(name.RefersTo, "$1:$1") {
			found = true
		}
	}
	if !found {
		t.Fatal("repeat header missing")
	}
	for ref, want := range map[string]struct{ raw, format string }{"A3": {"12.5", "0.0000"}, "A4": {"0.125", "0.0%"}} {
		raw, err := f.GetCellValue(sh, ref, excelize.Options{RawCellValue: true})
		if err != nil || raw != want.raw {
			t.Fatalf("raw %s=%q %v", ref, raw, err)
		}
		id, err := f.GetCellStyle(sh, ref)
		if err != nil {
			t.Fatal(err)
		}
		style, err := f.GetStyle(id)
		if err != nil || style.CustomNumFmt == nil || *style.CustomNumFmt != want.format {
			t.Fatalf("format %s=%+v %v", ref, style, err)
		}
	}
	var metadata struct {
		Format struct {
			Level int `xml:"outlineLevelRow,attr"`
		} `xml:"sheetFormatPr"`
		Rows []struct {
			R         int  `xml:"r,attr"`
			Collapsed bool `xml:"collapsed,attr"`
		} `xml:"sheetData>row"`
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range z.File {
		if entry.Name != "xl/worksheets/sheet1.xml" {
			continue
		}
		r, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(io.LimitReader(r, 1<<20))
		closeErr := r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		if err = xml.Unmarshal(raw, &metadata); err != nil {
			t.Fatal(err)
		}
	}
	if metadata.Format.Level != 2 {
		t.Fatalf("sheet outline max=%d", metadata.Format.Level)
	}
	marked := false
	for _, row := range metadata.Rows {
		if row.Collapsed {
			if row.R != 2 {
				t.Fatalf("wrong summary row: %+v", row)
			}
			marked = true
		}
	}
	if !marked {
		t.Fatal("native collapsed summary marker missing")
	}
	if a.Text != "raw a" || a.Value != 12.5 || d.Groups[1].Expanded {
		t.Fatal("export mutated source")
	}
}

func TestTabularExportXLSXCollapsedParentWins(t *testing.T) {
	d := NewDocument()
	for r := 0; r < 4; r++ {
		d.GetOrCreateCell(r, 0).Text = "data"
	}
	d.Groups = []RowGroup{{Start: 0, HeaderEnd: 0, Level: 1, Expanded: false}, {Start: 1, HeaderEnd: 1, Level: 2, Expanded: true}}
	data, err := d.XLSX()
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	}()
	for row := 2; row <= 4; row++ {
		visible, err := f.GetRowVisible(f.GetSheetName(0), row)
		if err != nil || visible {
			t.Fatalf("parent collapse lost row%d: %v %v", row, visible, err)
		}
	}
}
