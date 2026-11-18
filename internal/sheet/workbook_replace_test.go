package sheet

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func TestWorkbookUpdateXLSXFilePreservesArchive(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "архив.xlsx")
	initial := excelize.NewFile()
	require.NoError(t, initial.SetSheetName("Sheet1", "Октябрь"))
	for _, name := range []string{"Итого", "07Итого", "14Итого", "21Итого", "Справка"} {
		_, err := initial.NewSheet(name)
		require.NoError(t, err)
		require.NoError(t, initial.SetCellStr(name, "Z30", "прежняя строка"))
	}
	require.NoError(t, initial.SetCellFormula("Октябрь", "A1", "'Итого'!A1+1"))
	require.NoError(t, initial.SetCellStr("Октябрь", "B2", "прошлый месяц"))
	preservedStyle, err := initial.NewStyle(&excelize.Style{Font: &excelize.Font{Italic: true, Color: "AB12CD"}})
	require.NoError(t, err)
	require.NoError(t, initial.SetCellStyle("Октябрь", "B2", "C2", preservedStyle))
	require.NoError(t, initial.MergeCell("Октябрь", "B2", "C2"))
	require.NoError(t, initial.SetColWidth("Октябрь", "B", "B", 29))
	require.NoError(t, initial.SaveAs(filename))
	require.NoError(t, initial.Close())

	update := func(value int) []byte {
		book := NewWorkbook()
		for _, name := range []string{"Итого", "07Итого", "14Итого", "21Итого", "Ноябрь"} {
			doc := NewDocument()
			doc.SetCell(0, 0, "")
			cell := doc.GetCell(0, 0)
			cell.Value, cell.Bold, cell.ColSpan = value, true, 2
			doc.ColWidths[1] = 145
			doc.RowHeights[1] = 40
			require.NoError(t, book.AddSheet(name, doc))
		}
		got, err := book.UpdateXLSXFile(t.Context(), dir, filename)
		require.NoError(t, err)
		disk, err := os.ReadFile(filename)
		require.NoError(t, err)
		require.Equal(t, disk, got)
		return got
	}
	_ = update(41)
	data := update(42)
	f, err := excelize.OpenReader(bytes.NewReader(data))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, f.Close()) })
	require.Equal(t, []string{"Октябрь", "Итого", "07Итого", "14Итого", "21Итого", "Справка", "Ноябрь"}, f.GetSheetList())
	for _, name := range []string{"Итого", "07Итого", "14Итого", "21Итого", "Ноябрь"} {
		v, err := f.GetCellValue(name, "A1")
		require.NoError(t, err)
		require.Equal(t, "42", v)
		v, err = f.GetCellValue(name, "Z30")
		require.NoError(t, err)
		require.Empty(t, v)
		styleID, err := f.GetCellStyle(name, "A1")
		require.NoError(t, err)
		style, err := f.GetStyle(styleID)
		require.NoError(t, err)
		require.True(t, style.Font.Bold)
		merges, err := f.GetMergeCells(name)
		require.NoError(t, err)
		require.Len(t, merges, 1)
		require.Equal(t, "B1", merges[0].GetEndAxis())
		height, err := f.GetRowHeight(name, 1)
		require.NoError(t, err)
		require.Equal(t, 30.0, height)
		width, err := f.GetColWidth(name, "A")
		require.NoError(t, err)
		require.InDelta(t, 20, width, 0.1)
	}
	formula, err := f.GetCellFormula("Октябрь", "A1")
	require.NoError(t, err)
	require.Equal(t, "'Итого'!A1+1", formula)
	styleID, err := f.GetCellStyle("Октябрь", "B2")
	require.NoError(t, err)
	require.Equal(t, preservedStyle, styleID)
	merges, err := f.GetMergeCells("Октябрь")
	require.NoError(t, err)
	require.Len(t, merges, 1)
	require.Equal(t, "C2", merges[0].GetEndAxis())
	width, err := f.GetColWidth("Октябрь", "B")
	require.NoError(t, err)
	require.Equal(t, 29.0, width)
	value, err := f.GetCellValue("Справка", "Z30")
	require.NoError(t, err)
	require.Equal(t, "прежняя строка", value)
}

func TestWorkbookUpdateXLSXFileRefusesUnsafeAndConflictingWrites(t *testing.T) {
	dir := t.TempDir()
	invalid := []byte("broken file")
	filename := "итоги.xlsx"
	path := filepath.Join(dir, filename)
	require.NoError(t, os.WriteFile(path, invalid, 0o600))
	b := NewWorkbook()
	require.NoError(t, b.AddSheet("Лист", NewDocument()))
	for _, bad := range []string{"../итоги.xlsx", "missing.xlsx", "sub/итоги.xlsx", filepath.Join(t.TempDir(), filename), filename} {
		_, err := b.UpdateXLSXFile(t.Context(), dir, bad)
		require.Error(t, err, "%q", bad)
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, invalid, data)
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	lease, err := lockWorkbookDirectory(root)
	require.NoError(t, err)
	_, err = b.UpdateXLSXFile(t.Context(), dir, filename)
	require.ErrorContains(t, err, "другая операция")
	require.NoError(t, lease.Close())
	lease, err = lockWorkbookDirectory(root)
	require.NoError(t, err)
	require.NoError(t, lease.Close())
}
