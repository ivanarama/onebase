package sheet

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"
)

// Workbook limits apply to the complete export, rather than separately to
// each sheet. Rectangles count sparse cells and merge spans as render work.
const (
	MaxWorkbookSheets    = 256
	MaxWorkbookCells     = 1_000_000
	MaxWorkbookTextBytes = 16 << 20
	maxExcelRows         = 1_048_576
	maxExcelColumns      = 16_384
)

type workbookSheet struct {
	name string
	doc  *Document
}

// Workbook owns snapshots: a caller can clear/reuse the source document after
// AddSheet without changing a sheet already added to the workbook.
type Workbook struct {
	sheets  []workbookSheet
	cells   int64
	objects int64
	text    int64
}

func NewWorkbook() *Workbook { return &Workbook{} }

func (b *Workbook) SheetCount() int { return len(b.sheets) }

func validWorkbookSheetName(name string) bool {
	if !utf8.ValidString(name) || strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 31 ||
		strings.HasPrefix(name, "'") || strings.HasSuffix(name, "'") ||
		strings.ContainsAny(name, `[]:*?/\`) {
		return false
	}
	return !strings.ContainsFunc(name, unicode.IsControl)
}

// AddSheet validates first and changes the workbook only after all checks pass.
// Names are explicit and never truncated, sanitized or silently deduplicated.
func (b *Workbook) AddSheet(name string, doc *Document) error {
	if !validWorkbookSheetName(name) {
		return fmt.Errorf("invalid XLSX sheet name %q (1-31 characters; no []:*?/\\, control characters or edge apostrophes)", name)
	}
	for _, sh := range b.sheets {
		if strings.EqualFold(name, sh.name) {
			return fmt.Errorf("duplicate XLSX sheet name %q", name)
		}
	}
	if len(b.sheets) >= MaxWorkbookSheets {
		return fmt.Errorf("XLSX workbook exceeds %d sheets", MaxWorkbookSheets)
	}
	cells, objects, text, err := workbookDocumentBudget(doc)
	if err != nil {
		return err
	}
	if cells > MaxWorkbookCells-b.cells || objects > MaxWorkbookCells-b.objects {
		return fmt.Errorf("XLSX workbook exceeds the total %d cell/geometry budget", MaxWorkbookCells)
	}
	if text > MaxWorkbookTextBytes-b.text {
		return fmt.Errorf("XLSX workbook exceeds the total %d text byte budget", MaxWorkbookTextBytes)
	}
	b.sheets = append(b.sheets, workbookSheet{name: name, doc: snapshotWorkbookDocument(doc)})
	b.cells += cells
	b.objects += objects
	b.text += text
	return nil
}

func workbookDocumentBudget(doc *Document) (cells, objects, text int64, err error) {
	if doc == nil {
		return 0, 0, 0, fmt.Errorf("XLSX sheet requires a tabular document")
	}
	text = int64(len(doc.Page.Format)) + int64(len(doc.Page.Orientation))
	if doc.FreezeRows < 0 || doc.FreezeRows >= maxExcelRows || doc.FreezeCols < 0 || doc.FreezeCols >= maxExcelColumns {
		return 0, 0, 0, fmt.Errorf("XLSX freeze pane is outside Excel bounds")
	}
	for key, cell := range doc.Cells {
		if cell == nil || key.Row < 0 || key.Col < 0 || key.Row >= maxExcelRows || key.Col >= maxExcelColumns ||
			max(cell.RowSpan, 1) > maxExcelRows-key.Row || max(cell.ColSpan, 1) > maxExcelColumns-key.Col {
			return 0, 0, 0, fmt.Errorf("XLSX cell or merge span is outside Excel bounds")
		}
		text += int64(len(cell.Text)) + int64(len(cell.DisplayText())) + int64(len(cell.RichHTML)) +
			int64(len(cell.FontFamily)) + int64(len(cell.Format)) + int64(len(cell.Picture)) +
			int64(len(cell.TextColor)) + int64(len(cell.BackColor))
		if text > MaxWorkbookTextBytes {
			return 0, 0, 0, fmt.Errorf("XLSX document exceeds the text byte budget")
		}
	}
	for col := range doc.ColWidths {
		if col < 1 || col > maxExcelColumns {
			return 0, 0, 0, fmt.Errorf("XLSX column width is outside Excel bounds")
		}
	}
	for row := range doc.RowHeights {
		if row < 1 || row > maxExcelRows {
			return 0, 0, 0, fmt.Errorf("XLSX row height is outside Excel bounds")
		}
	}
	if doc.RepeatHeader && doc.HeaderArea != nil && (doc.HeaderArea.Rows() < 1 || doc.HeaderArea.Rows() > maxExcelRows) {
		return 0, 0, 0, fmt.Errorf("XLSX repeat header is outside Excel bounds")
	}
	if len(doc.Cells) > 0 {
		r, c := doc.ContentBounds()
		cells = int64(r+1) * int64(c+1)
		// xlsxView visits every member of every row group. Count that work,
		// including overlapping groups, in the same workbook-wide budget.
		for _, group := range doc.ResolvedGroups() {
			header, end := max(group.HeaderEnd, group.Start), min(group.End, r)
			if header >= 0 && header < end {
				cells += int64(end - header)
			}
			if cells > MaxWorkbookCells {
				return 0, 0, 0, fmt.Errorf("XLSX workbook exceeds the cell/group work budget")
			}
		}
	}
	for _, group := range doc.Groups {
		text += int64(len(group.Title))
	}
	objects = int64(len(doc.Cells)) + int64(len(doc.ColWidths)) + int64(len(doc.RowHeights)) + int64(len(doc.Groups)) + int64(len(doc.PageBreaks))
	return cells, objects, text, nil
}

func snapshotWorkbookDocument(doc *Document) *Document {
	copyDoc := *doc
	copyDoc.Cells = make(map[CellKey]*Cell, len(doc.Cells))
	for key, original := range doc.Cells {
		cell := *original
		// Only immutable XLSX scalar values are retained by reference. Other
		// values become their current display text, matching setXLSXValue.
		switch cell.Value.(type) {
		case float64, int, int64, decimal.Decimal, time.Time:
		default:
			cell.Value = original.DisplayText()
			cell.Text = original.DisplayText()
		}
		// Drilldown callbacks/payloads are not workbook data or capabilities.
		cell.Drilldown, cell.DrilldownPayload, cell.DrilldownSource = nil, nil, nil
		copyDoc.Cells[key] = &cell
	}
	copyDoc.ColWidths = maps.Clone(doc.ColWidths)
	copyDoc.RowHeights = maps.Clone(doc.RowHeights)
	copyDoc.Groups = slices.Clone(doc.Groups)
	copyDoc.PageBreaks = slices.Clone(doc.PageBreaks)
	if doc.HeaderArea != nil {
		header := *doc.HeaderArea
		header.Cells = nil // XLSX print titles use only the snapshotted row bounds.
		copyDoc.HeaderArea = &header
	}
	return &copyDoc
}

// XLSX writes all snapshots into one Excelize file. Every sheet uses the same
// writer as Document.XLSX; no independent workbooks are copied or ZIP-merged.
func (b *Workbook) XLSX() (data []byte, err error) {
	if len(b.sheets) == 0 {
		return nil, fmt.Errorf("XLSX workbook must contain at least one sheet")
	}
	f := excelize.NewFile()
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			data = nil
			err = errors.Join(err, closeErr)
		}
	}()
	outlines := map[string]xlsxSheetOutline{}
	for index, sh := range b.sheets {
		if index == 0 {
			err = f.SetSheetName("Sheet1", sh.name)
		} else {
			_, err = f.NewSheet(sh.name)
		}
		if err != nil {
			return nil, err
		}
		collapsed, level, writeErr := sh.doc.writeXLSXSheet(f, sh.name)
		if writeErr != nil {
			return nil, writeErr
		}
		if level > 0 {
			outlines[fmt.Sprintf("xl/worksheets/sheet%d.xml", index+1)] = xlsxSheetOutline{collapsed: collapsed, level: level}
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	if len(outlines) > 0 {
		return xlsxWorkbookOutlineXML(buf.Bytes(), outlines)
	}
	return buf.Bytes(), nil
}
