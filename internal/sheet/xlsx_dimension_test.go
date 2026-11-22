package sheet

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"testing"
)

func TestDocumentXLSXDimension(t *testing.T) {
	for _, mode := range []string{"empty", "single", "dense", "sparse-offset", "merge-span", "styled-tail", "collapsed", "autoscale", "format-maps-only", "kpi-53893"} {
		t.Run(mode, func(t *testing.T) {
			d := NewDocument()
			want, edge, numeric := "A1", "", ""
			switch mode {
			case "empty":
			case "single":
				d.GetOrCreateCell(0, 0).Value = 42
				edge, numeric = "A1", "42"
			case "dense":
				for row := 0; row < 2; row++ {
					for col := 0; col < 3; col++ {
						d.GetOrCreateCell(row, col).Value = 42
					}
				}
				want, edge, numeric = "A1:C2", "C2", "42"
			case "sparse-offset":
				d.GetOrCreateCell(829, 66).Value = 42
				want, edge, numeric = "A1:BO830", "BO830", "42"
			case "merge-span":
				c := d.GetOrCreateCell(2, 3)
				c.Text, c.RowSpan, c.ColSpan = "merged", 3, 5
				want, edge = "A1:H5", "H5"
			case "styled-tail":
				d.GetOrCreateCell(0, 0).Value = 42
				d.GetOrCreateCell(829, 66).BackColor = "#fffaf0"
				want, edge = "A1:BO830", "BO830"
			case "collapsed":
				for row := 0; row < 6; row++ {
					d.GetOrCreateCell(row, 3).Value = 42
				}
				d.Groups = []RowGroup{{Start: 0, HeaderEnd: 0, Level: 1, Expanded: false}, {Start: 1, HeaderEnd: 1, Level: 2, Expanded: true}}
				want, edge, numeric = "A1:D6", "D6", "42"
			case "autoscale":
				d.AutoScale = true
				d.GetOrCreateCell(50, 66).Value = 42
				want, edge, numeric = "A1:BO51", "BO51", "42"
			case "format-maps-only":
				d.SetColumnWidth(100, 120)
				d.SetRowHeight(900, 20)
			case "kpi-53893":
				for row := 0; row < 18; row++ {
					for col := 0; col < 67; col++ {
						d.GetOrCreateCell(row, col).Value = 42
					}
				}
				for col := 0; col < 36; col++ {
					d.GetOrCreateCell(18, col).Value = 42
				}
				d.GetOrCreateCell(19, 0).Value = 42
				for row := 20; row < 830; row++ {
					for col := 0; col < 65; col++ {
						d.GetOrCreateCell(row, col).Value = 42
					}
				}
				if len(d.Cells) != 53893 {
					t.Fatal("wrong KPI fixture size")
				}
				want, edge, numeric = "A1:BO830", "BM830", "42"
			}
			data, err := d.XLSX()
			if err != nil {
				t.Fatal(err)
			}
			z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			var worksheet struct {
				Dimension struct {
					Ref string `xml:"ref,attr"`
				} `xml:"dimension"`
				Rows []struct {
					Collapsed bool `xml:"collapsed,attr"`
					Cells     []struct {
						Ref   string `xml:"r,attr"`
						Value string `xml:"v"`
					} `xml:"c"`
				} `xml:"sheetData>row"`
				Merges []struct {
					Ref string `xml:"ref,attr"`
				} `xml:"mergeCells>mergeCell"`
				PageSetup struct {
					Width  string `xml:"fitToWidth,attr"`
					Height string `xml:"fitToHeight,attr"`
				} `xml:"pageSetup"`
			}
			found := false
			for _, entry := range z.File {
				if entry.Name != "xl/worksheets/sheet1.xml" {
					continue
				}
				r, err := entry.Open()
				if err != nil {
					t.Fatal(err)
				}
				err = xml.NewDecoder(r).Decode(&worksheet)
				closeErr := r.Close()
				if err != nil {
					t.Fatal(err)
				}
				if closeErr != nil {
					t.Fatal(closeErr)
				}
				found = true
			}
			if !found || worksheet.Dimension.Ref != want {
				t.Fatalf("ZIP dimension=%q, want %q", worksheet.Dimension.Ref, want)
			}
			foundEdge, collapsed, cells := edge == "", false, 0
			for _, row := range worksheet.Rows {
				collapsed = collapsed || row.Collapsed
				for _, cell := range row.Cells {
					cells++
					if cell.Ref == edge {
						foundEdge = true
						if numeric != "" && cell.Value != numeric {
							t.Fatal("numeric value changed")
						}
					}
				}
			}
			if !foundEdge {
				t.Fatal("range edge cell missing from ZIP")
			}
			if mode == "merge-span" && (len(worksheet.Merges) != 1 || worksheet.Merges[0].Ref != "D3:H5") {
				t.Fatal("merge changed")
			}
			if mode == "collapsed" && !collapsed {
				t.Fatal("outline metadata lost")
			}
			if mode == "autoscale" && (worksheet.PageSetup.Width != "1" || worksheet.PageSetup.Height != "0") {
				t.Fatal("print fit changed")
			}
			if mode == "kpi-53893" && cells != 53893 {
				t.Fatalf("KPI cells truncated: %d", cells)
			}
		})
	}
}
