package xlsxtemplate

import (
	"bytes"
	"testing"

	"github.com/ivantit66/onebase/internal/printform"
	"github.com/xuri/excelize/v2"
)

func TestRenderBytesPreservesWorkbookAndRepeatsRows(t *testing.T) {
	f := excelize.NewFile()
	sheet := "Sheet1"
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(f.SetCellStr(sheet, "A1", "{{Контрагент.Наименование}}"))
	must(f.SetCellStr(sheet, "B1", "ИНН {{ИНН}}"))
	must(f.SetCellStr(sheet, "C1", "{{Сумма}}"))
	must(f.SetCellStr(sheet, "A3", "{{Товары.Наименование}}"))
	must(f.SetCellStr(sheet, "B3", "{{Товары.Количество}}"))
	must(f.SetCellStr(sheet, "D3", "строка {{@row}}"))
	must(f.MergeCell(sheet, "D3", "E3"))
	must(f.SetCellStr(sheet, "A4", "Итого: {{Итог.Товары.Количество | number:0}}"))
	must(f.SetColWidth(sheet, "A", "A", 24))
	style, err := f.NewStyle(&excelize.Style{Fill: excelize.Fill{Type: "pattern", Color: []string{"#FFFF00"}, Pattern: 1}})
	must(err)
	must(f.SetCellStyle(sheet, "A3", "E3", style))
	must(f.SetDefinedName(&excelize.DefinedName{Name: "OB_Строка", RefersTo: "Sheet1!$A$3:$E$3"}))

	var input bytes.Buffer
	must(f.Write(&input))
	must(f.Close())

	result, err := RenderBytes(input.Bytes(), &printform.RenderContext{
		EntityName: "Контрагент",
		Document: map[string]any{
			"Наименование": "ООО Ромашка",
			"ИНН":          "7701000000",
			"Сумма":        1250.5,
		},
		TableParts: map[string][]map[string]any{
			"Товары": {
				{"Наименование": "Бумага", "Количество": 2},
				{"Наименование": "Ручка", "Количество": 3},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	out, err := excelize.OpenReader(bytes.NewReader(result))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	assertCell(t, out, sheet, "A1", "ООО Ромашка")
	assertCell(t, out, sheet, "B1", "ИНН 7701000000")
	assertCell(t, out, sheet, "C1", "1250.5")
	assertCell(t, out, sheet, "A3", "Бумага")
	assertCell(t, out, sheet, "B3", "2")
	assertCell(t, out, sheet, "D3", "строка 1")
	assertCell(t, out, sheet, "A4", "Ручка")
	assertCell(t, out, sheet, "B4", "3")
	assertCell(t, out, sheet, "D4", "строка 2")
	assertCell(t, out, sheet, "A5", "Итого: 5")
	if typ, err := out.GetCellType(sheet, "C1"); err != nil || (typ != excelize.CellTypeNumber && typ != excelize.CellTypeUnset) {
		t.Errorf("C1 type = %v, %v; want number", typ, err)
	}
	if got, _ := out.GetCellStyle(sheet, "A4"); got != style {
		t.Errorf("duplicated style = %d, want %d", got, style)
	}
	merges, err := out.GetMergeCells(sheet)
	if err != nil || len(merges) != 2 || merges[1].GetStartAxis() != "D4" || merges[1].GetEndAxis() != "E4" {
		t.Errorf("duplicated merges = %+v, err=%v", merges, err)
	}
	if width, err := out.GetColWidth(sheet, "A"); err != nil || width != 24 {
		t.Errorf("column width = %v, %v; want 24", width, err)
	}
	if len(out.GetDefinedName()) == 0 {
		t.Error("defined names were lost")
	}
}

func assertCell(t *testing.T, f *excelize.File, sheet, axis, want string) {
	t.Helper()
	got, err := f.GetCellValue(sheet, axis)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("%s = %q, want %q", axis, got, want)
	}
}

func TestRenderBytesEmptyTablePartLeavesBlankTemplateRow(t *testing.T) {
	f := excelize.NewFile()
	if err := f.SetCellStr("Sheet1", "A1", "{{Строки.Значение}}"); err != nil {
		t.Fatal(err)
	}
	var input bytes.Buffer
	if err := f.Write(&input); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	result, err := RenderBytes(input.Bytes(), &printform.RenderContext{
		TableParts: map[string][]map[string]any{"Строки": {}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := excelize.OpenReader(bytes.NewReader(result))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	assertCell(t, out, "Sheet1", "A1", "")
}
