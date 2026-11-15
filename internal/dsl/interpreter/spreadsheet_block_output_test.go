package interpreter

import (
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/printform"
	"github.com/ivantit66/onebase/internal/runtime"
	"testing"
)

func TestSpreadsheetBlockOutputDSL(t *testing.T) {
	for _, tc := range []struct {
		name      string
		rightRows int
	}{{"equal_height", 2}, {"taller_right", 4}, {"shorter_right", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			area := func(name, text string, rows int) *printform.LayoutArea {
				a := &printform.LayoutArea{Name: name}
				for i := 0; i < rows; i++ {
					a.Rows = append(a.Rows, printform.LayoutRow{Cells: []printform.LayoutCell{{Text: text}}})
				}
				return a
			}
			lt := &printform.LayoutTemplate{Name: "Проба", Columns: []printform.LayoutColumn{{Width: "84px"}}, Areas: []*printform.LayoutArea{area("Лево", "LEFT", 2), area("Право", "RIGHT", tc.rightRows), area("Низ", "NEXT", 1)}}
			src := `Процедура Main() Д=Новый ТабличныйДокумент;Д.ШиринаКолонки(3,150);Д.Вывести(Макет.ПолучитьОбласть("Лево"));Д.Присоединить(Макет.ПолучитьОбласть("Право"));Д.Присоединить(Макет.ПолучитьОбласть("Право"));Д.Вывести(Макет.ПолучитьОбласть("Низ"));Объект.Result=Д;КонецПроцедуры`
			p, e := parser.New(lexer.New(src, "block.os")).ParseProgram()
			if e != nil {
				t.Fatal(e)
			}
			o := runtime.NewObject("Own", metadata.KindDocument)
			i := New()
			vars := NewSpreadsheetFunctions()
			vars["Макет"] = NewMaket(lt)
			if e = i.Run(p.Procedures[0], o, vars); e != nil {
				t.Fatal(e)
			}
			d, ok := o.Get("Result").(*SpreadsheetDocument)
			if !ok {
				t.Fatal("no document")
			}
			for c := 1; c <= 2; c++ {
				v := d.Doc.GetCell(0, c)
				if v == nil || v.Text != "RIGHT" {
					t.Fatal("horizontal area on wrong row/column")
				}
			}
			bottom := max(2, tc.rightRows)
			v := d.Doc.GetCell(bottom, 0)
			if v == nil || v.Text != "NEXT" {
				t.Fatal("next Put did not reset column/block height")
			}
			if d.Doc.ColumnWidth(2) != 84 || d.Doc.ColumnWidth(3) != 150 {
				t.Fatal("shifted layout width or explicit width precedence lost")
			}
		})
	}
}
