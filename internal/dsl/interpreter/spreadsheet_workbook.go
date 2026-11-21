package interpreter

import (
	"encoding/base64"
	"strings"

	"github.com/ivantit66/onebase/internal/sheet"
)

// SpreadsheetWorkbook is a server-side collection of named document snapshots.
// Write returns base64 data, like SpreadsheetDocument.Write, and never opens a path.
type SpreadsheetWorkbook struct {
	book *sheet.Workbook
}

func newSpreadsheetWorkbook(args []any) any {
	if len(args) != 0 {
		RaiseUserError("SpreadsheetWorkbook constructor takes no arguments")
	}
	return &SpreadsheetWorkbook{book: sheet.NewWorkbook()}
}

func (b *SpreadsheetWorkbook) KnownMethods() (string, []string) {
	return "SpreadsheetWorkbook", []string{"ДобавитьЛист", "AddSheet", "Количество", "Count", "Записать", "Write"}
}

func (b *SpreadsheetWorkbook) CallMethod(name string, args []any) any {
	switch strings.ToLower(name) {
	case "добавитьлист", "addsheet":
		if len(args) != 2 {
			RaiseUserError("SpreadsheetWorkbook.AddSheet requires a name and a tabular document")
		}
		name, ok := args[0].(string)
		if !ok {
			RaiseUserError("SpreadsheetWorkbook sheet name must be a string")
		}
		doc, ok := args[1].(*SpreadsheetDocument)
		if !ok || doc == nil {
			RaiseUserError("SpreadsheetWorkbook.AddSheet requires a tabular document")
		}
		if err := b.book.AddSheet(name, doc.Doc); err != nil {
			RaiseUserError(err.Error())
		}
		return nil
	case "количество", "count":
		if len(args) != 0 {
			RaiseUserError("SpreadsheetWorkbook.Count takes no arguments")
		}
		return float64(b.book.SheetCount())
	case "записать", "write":
		if len(args) != 1 && len(args) != 2 {
			RaiseUserError("SpreadsheetWorkbook.Write requires a filename and optional xlsx format")
		}
		if _, ok := args[0].(string); !ok {
			RaiseUserError("SpreadsheetWorkbook filename must be a string")
		}
		if len(args) == 2 && !strings.EqualFold(strArg(args, 1), "xlsx") {
			RaiseUserError("SpreadsheetWorkbook supports only the xlsx format")
		}
		data, err := b.book.XLSX()
		if err != nil {
			RaiseUserError(err.Error())
		}
		return base64.StdEncoding.EncodeToString(data)
	}
	RaiseUserError("Unknown SpreadsheetWorkbook method: " + name)
	return nil
}
