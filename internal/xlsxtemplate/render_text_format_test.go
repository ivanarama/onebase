package xlsxtemplate

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/printform"
	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"
)

// Exercise the exported renderer and inspect the serialized workbook, including
// both representations of the author's text format and ordinary numeric cells.
func TestRenderBytesExplicitTextNumericTags(t *testing.T) {
	for _, customText := range []bool{false, true} {
		name := "NumFmt49"
		if customText {
			name = "CustomNumFmt"
		}
		t.Run(name, func(t *testing.T) {
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			f := excelize.NewFile()
			textFormat := "@"
			style := &excelize.Style{NumFmt: 49, Font: &excelize.Font{Bold: true, Color: "123456"}, Fill: excelize.Fill{Type: "pattern", Color: []string{"FFFF00"}, Pattern: 1}, Alignment: &excelize.Alignment{Horizontal: "right"}}
			if customText {
				style.NumFmt = 0
				style.CustomNumFmt = &textFormat
			}
			textID, err := f.NewStyle(style)
			must(err)
			cases := []struct{ axis, tag, want string }{
				{"A1", "Amount | number:2", "1234.50"},
				{"A2", "Amount | number:0", "1235"},
				{"A3", "Whole | currency", "100.00"},
				{"A4", "Negative | number:2", "-1.50"},
				{"A5", "Code", "0012345678"},
				{"A6", "Amount | number:2147483647", "1234.500000000000000000000000000000"},
				{"A7", "Итог.Items.Amount | number:2", "3.50"},
				{"A9", "Items.Amount | currency", "1.00"},
			}
			for _, tc := range cases {
				must(f.SetCellStr("Sheet1", tc.axis, "{{"+tc.tag+"}}"))
				must(f.SetCellStyle("Sheet1", tc.axis, tc.axis, textID))
			}
			must(f.SetCellStr("Sheet1", "B1", "{{Amount | number:2}}"))
			must(f.SetCellStr("Sheet1", "B2", "{{Amount | number:2}}"))
			mask := `0.000" unit"`
			maskID, err := f.NewStyle(&excelize.Style{CustomNumFmt: &mask})
			must(err)
			must(f.SetCellStyle("Sheet1", "B2", "B2", maskID))
			must(f.SetCellFormula("Sheet1", "C1", "SUM(B1:B2)"))
			before, err := f.GetStyle(textID)
			must(err)
			var input bytes.Buffer
			must(f.Write(&input))
			must(f.Close())
			data, err := RenderBytes(input.Bytes(), &printform.RenderContext{
				Document:   map[string]any{"Amount": decimal.RequireFromString("1234.5"), "Whole": 100, "Negative": decimal.RequireFromString("-1.5"), "Code": "0012345678"},
				TableParts: map[string][]map[string]any{"Items": {{"Amount": decimal.RequireFromString("1")}, {"Amount": decimal.RequireFromString("2.5")}}},
			})
			must(err)
			out, err := excelize.OpenReader(bytes.NewReader(data))
			must(err)
			defer func() { must(out.Close()) }()
			for _, tc := range append(cases, struct{ axis, tag, want string }{"A10", "", "2.50"}) {
				assertTextCell(t, out, "Sheet1", tc.axis, tc.want)
				id, err := out.GetCellStyle("Sheet1", tc.axis)
				must(err)
				after, err := out.GetStyle(id)
				must(err)
				if !reflect.DeepEqual(before, after) {
					t.Errorf("%s: text format/style changed: before=%+v after=%+v", tc.axis, before, after)
				}
			}
			for _, axis := range []string{"B1", "B2"} {
				assertNumberCell(t, out, "Sheet1", axis, "1234.5")
			}
			assertNumFmt(t, out, "Sheet1", "B1", "0.00")
			assertNumFmt(t, out, "Sheet1", "B2", mask)
			formula, err := out.GetCellFormula("Sheet1", "C1")
			must(err)
			if formula != "SUM(B1:B2)" {
				t.Errorf("formula changed: %q", formula)
			}
			got, err := out.CalcCellValue("Sheet1", "C1")
			must(err)
			if strings.TrimSpace(got) != "2469" {
				t.Errorf("numeric formula = %q, want 2469", got)
			}
		})
	}
}
