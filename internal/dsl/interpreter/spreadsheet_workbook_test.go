package interpreter_test

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/sheet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func openWorkbookResult(t *testing.T, value any) (*excelize.File, []byte) {
	t.Helper()
	encoded, ok := value.(string)
	require.True(t, ok)
	data, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	f, err := excelize.OpenReader(bytes.NewReader(data))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, f.Close()) })
	return f, data
}

// The published DSL entry point, rather than a private workbook method, must
// preserve numeric values, styles and sheet order when a document is reused.
func TestSpreadsheetWorkbookDSLTwoNamedSnapshots(t *testing.T) {
	value := runHTTPSrc(t, `Функция Тест()
Книга = Новый КнигаТабличныхДокументов;
Д = Новый ТабличныйДокумент;
Д.Ячейка(1,1).Значение = 111;
Д.Ячейка(1,1).ЦветФона = "#ff0000";
Книга.ДобавитьЛист("СМ", Д);
Д.Очистить();
Д.Ячейка(1,1).Значение = 222;
Д.Ячейка(1,1).ЦветФона = "#00ff00";
Книга.ДобавитьЛист("ХД", Д);
Д.Ячейка(1,1).Значение = 999;
	Если Книга.Количество() <> 2 Тогда ВызватьИсключение "sheet count";КонецЕсли;
Возврат Книга.Записать("направления.xlsx", "xlsx");
КонецФункции`, withSpreadsheet(map[string]any{}))
	f, _ := openWorkbookResult(t, value)
	require.Equal(t, []string{"СМ", "ХД"}, f.GetSheetList())
	for _, tc := range []struct{ name, number, color string }{{"СМ", "111", "FF0000"}, {"ХД", "222", "00FF00"}} {
		got, err := f.GetCellValue(tc.name, "A1", excelize.Options{RawCellValue: true})
		require.NoError(t, err)
		assert.Equal(t, tc.number, got)
		kind, err := f.GetCellType(tc.name, "A1")
		require.NoError(t, err)
		// OOXML's absent t attribute is numeric too; Excelize reports it as
		// CellTypeUnset. Shared/inline strings must still fail this assertion.
		assert.True(t, kind == excelize.CellTypeNumber || kind == excelize.CellTypeUnset)
		id, err := f.GetCellStyle(tc.name, "A1")
		require.NoError(t, err)
		style, err := f.GetStyle(id)
		require.NoError(t, err)
		require.NotEmpty(t, style.Fill.Color)
		assert.Equal(t, tc.color, strings.ToUpper(style.Fill.Color[0]))
	}
	if os.Getenv("ONEBASE_TEST_WORKBOOK_LOG") == "1" {
		t.Logf("WORKBOOK_XLSX_BASE64:%s", value)
	}
}

func TestSpreadsheetWorkbookDSLLayoutAndGroups(t *testing.T) {
	vars := map[string]any{}
	for index, key := range []string{"Первый", "Второй"} {
		doc := interpreter.NewSpreadsheetDocument()
		for row := range 4 {
			cell := doc.Doc.GetOrCreateCell(row, 0)
			cell.Value = 111 + index*111 + row
		}
		doc.Doc.GetCell(0, 0).ColSpan = 2
		doc.Doc.ColWidths[1], doc.Doc.RowHeights[1] = 145, 24
		doc.Doc.FreezeRows, doc.Doc.FreezeCols = 2, 1
		doc.Doc.Page.Orientation, doc.Doc.AutoScale = "landscape", true
		doc.Doc.RepeatHeader, doc.Doc.HeaderArea = true, sheet.NewArea(0, 0, 1, 1)
		doc.Doc.Groups = []sheet.RowGroup{{Start: 0, HeaderEnd: 0, End: 3, Level: 1, Closed: true, Expanded: false}}
		vars[key] = doc
	}
	value := runHTTPSrc(t, `Функция Тест()
Книга = Новый SpreadsheetWorkbook;
Книга.AddSheet("СМ", Первый);
Первый.Очистить();
Первый.ШиринаКолонки(1,10);
Книга.AddSheet("ХД", Второй);
Второй.Очистить();
Возврат Книга.Write("layout.xlsx");
КонецФункции`, withSpreadsheet(vars))
	f, data := openWorkbookResult(t, value)
	for _, name := range []string{"СМ", "ХД"} {
		width, err := f.GetColWidth(name, "A")
		require.NoError(t, err)
		assert.InDelta(t, 20, width, .01)
		height, err := f.GetRowHeight(name, 1)
		require.NoError(t, err)
		assert.InDelta(t, 18, height, .01)
		merges, err := f.GetMergeCells(name)
		require.NoError(t, err)
		require.Len(t, merges, 1)
		assert.Equal(t, "A1", merges[0].GetStartAxis())
		assert.Equal(t, "B1", merges[0].GetEndAxis())
		panes, err := f.GetPanes(name)
		require.NoError(t, err)
		assert.True(t, panes.Freeze)
		assert.Equal(t, "B3", panes.TopLeftCell)
		layout, err := f.GetPageLayout(name)
		require.NoError(t, err)
		require.NotNil(t, layout.Orientation)
		assert.Equal(t, "landscape", *layout.Orientation)
		require.NotNil(t, layout.FitToWidth)
		assert.Equal(t, 1, *layout.FitToWidth)
		require.NotNil(t, layout.FitToHeight)
		assert.Zero(t, *layout.FitToHeight)
		visible, err := f.GetRowVisible(name, 2)
		require.NoError(t, err)
		assert.False(t, visible)
		dimension, err := f.GetSheetDimension(name)
		require.NoError(t, err)
		assert.Equal(t, "A1:B4", dimension)
	}
	definitions := f.GetDefinedName()
	for _, name := range []string{"СМ", "ХД"} {
		found := false
		for _, definition := range definitions {
			if definition.Name == "_xlnm.Print_Titles" && definition.Scope == name {
				assert.Equal(t, "'"+name+"'!$1:$2", definition.RefersTo)
				found = true
			}
		}
		assert.True(t, found, "sheet-scoped print title missing: %s", name)
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	parts := 0
	for _, entry := range z.File {
		if entry.Name != "xl/worksheets/sheet1.xml" && entry.Name != "xl/worksheets/sheet2.xml" {
			continue
		}
		r, err := entry.Open()
		require.NoError(t, err)
		xml, err := io.ReadAll(r) //nolint:gosec // Only the tiny workbook generated in this test.
		closeErr := r.Close()
		require.NoError(t, err)
		require.NoError(t, closeErr)
		assert.Contains(t, string(xml), `collapsed="1"`)
		assert.Contains(t, string(xml), `outlineLevelRow="1"`)
		parts++
	}
	assert.Equal(t, 2, parts)
}

func TestSpreadsheetWorkbookDSLRejectsInvalidInputs(t *testing.T) {
	for _, tc := range []struct{ name, code, reason string }{
		{"empty", `Книга.Записать("empty.xlsx");`, "at least one sheet"},
		{"blank", `Книга.ДобавитьЛист(" ",Д);`, "invalid XLSX sheet name"},
		{"long", `Книга.ДобавитьЛист("` + strings.Repeat("Я", 32) + `",Д);`, "invalid XLSX sheet name"},
		{"invalid-character", `Книга.ДобавитьЛист("A/B",Д);`, "invalid XLSX sheet name"},
		{"apostrophe", `Книга.ДобавитьЛист("'A",Д);`, "invalid XLSX sheet name"},
		{"duplicate", `Книга.ДобавитьЛист("ОТК",Д);Книга.ДобавитьЛист("отк",Д);`, "duplicate XLSX sheet name"},
		{"wrong-document", `Книга.ДобавитьЛист("A",42);`, "requires a tabular document"},
		{"wrong-name", `Книга.ДобавитьЛист(42,Д);`, "name must be a string"},
		{"arity", `Книга.ДобавитьЛист("A");`, "requires a name"},
		{"format", `Книга.ДобавитьЛист("A",Д);Книга.Записать("x.pdf","pdf");`, "only the xlsx format"},
		{"count-arity", `Книга.Количество(1);`, "takes no arguments"},
		{"constructor", `Книга=Новый SpreadsheetWorkbook(1);`, "constructor takes no arguments"},
		{"row-bound", `Д.Ячейка(1048577,1).Значение=1;Книга.ДобавитьЛист("A",Д);`, "outside Excel bounds"},
		{"column-bound", `Д.Ячейка(1,16385).Значение=1;Книга.ДобавитьЛист("A",Д);`, "outside Excel bounds"},
		{"sparse-budget", `Д.Ячейка(1000001,1).Значение=1;Книга.ДобавитьЛист("A",Д);`, "budget"},
		{"global-budget", `Д.Ячейка(600000,1).Значение=1;Книга.ДобавитьЛист("A",Д);Книга.ДобавитьЛист("B",Д);`, "budget"},
		{"unknown-method", `Книга.WritX("x.xlsx");`, "SpreadsheetWorkbook"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := fmt.Sprintf(`Функция Тест()
Книга=Новый КнигаТабличныхДокументов;Д=Новый ТабличныйДокумент;
Попытка %s Исключение Возврат ОписаниеОшибки();КонецПопытки;
Возврат "NO_ERROR";КонецФункции`, tc.code)
			value := runHTTPSrc(t, src, withSpreadsheet(map[string]any{}))
			require.IsType(t, "", value)
			assert.Contains(t, value.(string), tc.reason)
		})
	}
}
