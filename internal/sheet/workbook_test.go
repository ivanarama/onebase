package sheet

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkbookResourceLimitsAreGlobalAndAtomic(t *testing.T) {
	book := NewWorkbook()
	doc := NewDocument()
	doc.GetOrCreateCell(MaxWorkbookCells-1, 0).Value = 1
	require.NoError(t, book.AddSheet("Exact", doc))
	require.NoError(t, book.AddSheet("Empty", NewDocument()))
	small := NewDocument()
	small.GetOrCreateCell(0, 0).Value = 2
	require.ErrorContains(t, book.AddSheet("TooMuch", small), "budget")
	require.Equal(t, 2, book.SheetCount())

	textBook := NewWorkbook()
	textDoc := NewDocument()
	textDoc.Page.Format, textDoc.Page.Orientation = "", ""
	textDoc.Cells[CellKey{}] = &Cell{Text: strings.Repeat("x", MaxWorkbookTextBytes/2)}
	require.NoError(t, textBook.AddSheet("Exact", textDoc))
	require.ErrorContains(t, textBook.AddSheet("TooMuch", small), "text byte budget")
	require.Equal(t, 1, textBook.SheetCount())
}

func TestWorkbookExcelBoundsIncludeMergesAndGeometry(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*Document)
	}{
		{"merge-row", func(d *Document) { d.GetOrCreateCell(0, 0).RowSpan = maxExcelRows + 1 }},
		{"merge-column", func(d *Document) { d.GetOrCreateCell(0, 0).ColSpan = maxExcelColumns + 1 }},
		{"negative-cell", func(d *Document) { d.GetOrCreateCell(-1, 0) }},
		{"width", func(d *Document) { d.ColWidths[maxExcelColumns+1] = 10 }},
		{"height", func(d *Document) { d.RowHeights[maxExcelRows+1] = 10 }},
		{"pane", func(d *Document) { d.FreezeRows = maxExcelRows }},
		{"nil-cell", func(d *Document) { d.Cells[CellKey{}] = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := NewDocument()
			tc.set(doc)
			book := NewWorkbook()
			require.Error(t, book.AddSheet("Invalid", doc))
			require.Zero(t, book.SheetCount())
		})
	}
}

func TestWorkbookNamesAndSheetBudget(t *testing.T) {
	book := NewWorkbook()
	for i := range MaxWorkbookSheets {
		require.NoError(t, book.AddSheet(strings.Repeat("Я", 30)+string(rune(0x4e00+i)), NewDocument()))
	}
	require.ErrorContains(t, book.AddSheet("Overflow", NewDocument()), "sheets")
	require.Equal(t, MaxWorkbookSheets, book.SheetCount())
	require.Error(t, NewWorkbook().AddSheet("Nil", nil))
	_, err := NewWorkbook().XLSX()
	require.Error(t, err)
}

func TestWorkbookGroupingWorkSharesCellBudget(t *testing.T) {
	doc := NewDocument()
	doc.GetOrCreateCell(600_000, 0).Value = 1
	doc.Groups = []RowGroup{{Start: 0, HeaderEnd: 0, End: 600_000, Level: 1, Closed: true}}
	require.ErrorContains(t, NewWorkbook().AddSheet("Grouped", doc), "group work budget")
}
