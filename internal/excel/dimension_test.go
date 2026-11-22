package excel

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestListXLSXUsedRange(t *testing.T) {
	for _, tc := range []struct {
		name string
		cols []string
		rows [][]any
		want string
	}{
		{"empty", nil, nil, "A1"},
		{"header-only", []string{"A", "B"}, nil, "A1:B1"},
		{"ragged-wide", []string{"A"}, [][]any{{1, 2, 3}, {4}}, "A1:C3"},
		{"styled-empty-tail", []string{"A", "B"}, [][]any{{"value", 1}, {nil, nil}}, "A1:B3"},
		{"height-only-tail", []string{"A", "B"}, [][]any{{"value", 1}, nil, nil}, "A1:B2"},
		{"no-header", nil, [][]any{{"value", 2}}, "A1:B2"},
	} {
		for _, mode := range []string{"buffered", "stream"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				var data []byte
				var err error
				if mode == "buffered" {
					data, err = ExportList(tc.cols, tc.rows)
				} else {
					var b bytes.Buffer
					err = WriteList(&b, tc.cols, tc.rows)
					data = b.Bytes()
				}
				if err != nil {
					t.Fatal(err)
				}
				z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, p := range z.File {
					if p.Name != "xl/worksheets/sheet1.xml" {
						continue
					}
					r, err := p.Open()
					if err != nil {
						t.Fatal(err)
					}
					raw, err := io.ReadAll(r)
					if closeErr := r.Close(); closeErr != nil {
						t.Fatal(closeErr)
					}
					if err != nil {
						t.Fatal(err)
					}
					var sheet struct {
						Dimension struct {
							Ref string `xml:"ref,attr"`
						} `xml:"dimension"`
					}
					if err := xml.Unmarshal(raw, &sheet); err != nil {
						t.Fatal(err)
					}
					if sheet.Dimension.Ref != tc.want {
						t.Fatalf("dimension=%q want%q", sheet.Dimension.Ref, tc.want)
					}
					found = true
				}
				if !found {
					t.Fatal("worksheet missing")
				}
				f, err := excelize.OpenReader(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := f.Close(); err != nil {
						t.Fatal(err)
					}
				}()
				if tc.name == "ragged-wide" {
					if v, err := f.GetCellValue("Лист1", "C2"); err != nil || v != "3" {
						t.Fatal("ragged data changed")
					}
				}
			})
		}
	}
}
