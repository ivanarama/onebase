package ui

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/sheet"
)

type workbookDownload struct {
	name string
	data []byte
}

func bindWorkbookCommands(ctx context.Context, w http.ResponseWriter, r *http.Request, vars map[string]any, output **workbookDownload) {
	makeCommand := func(replace bool) interpreter.BuiltinFunc {
		return func(args []any, _ string, _ int) (any, error) {
			if len(args) != 2 {
				return nil, fmt.Errorf("ожидались путь или имя файла XLSX и книга табличных документов")
			}
			filename, ok := args[0].(string)
			book, bookOK := args[1].(*interpreter.SpreadsheetWorkbook)
			if !ok || !bookOK || book == nil {
				return nil, fmt.Errorf("ожидались строка с именем файла XLSX и книга табличных документов")
			}
			name := filepath.Base(filename)
			if !sheet.ValidXLSXFileName(name) || (!replace && name != filename) {
				return nil, fmt.Errorf("некорректное имя файла XLSX")
			}
			if *output != nil {
				return nil, fmt.Errorf("одна команда формы может скачать одну книгу XLSX")
			}
			if _, err := ensureTabularOwner(w, r); err != nil {
				return nil, err
			}
			var data []byte
			var err error
			if replace {
				data, err = book.UpdateXLSXFile(ctx, os.Getenv("ONEBASE_REPORT_XLSX_DIR"), filename)
			} else {
				data, err = book.XLSX()
			}
			if err != nil {
				return nil, err
			}
			if len(data) > sheet.MaxUpdateXLSXBytes {
				return nil, fmt.Errorf("книга XLSX превышает 32 МиБ")
			}
			*output = &workbookDownload{name: name, data: data}
			return true, nil
		}
	}
	replace, create := makeCommand(true), makeCommand(false)
	vars["ЗаменитьЛистыExcel"], vars["ReplaceExcelSheets"] = replace, replace
	vars["СкачатьКнигуExcel"], vars["DownloadExcelWorkbook"] = create, create
}

func (s *Server) attachWorkbook(r *http.Request, scope tabularScope, download *workbookDownload, resp *formEventResponse) {
	if download == nil {
		return
	}
	owner, err := tabularOwner(r)
	if err != nil {
		return
	}
	resp.WorkbookDownloadURL = s.storeTabularResult(&tabularResult{scope: scope, owner: owner, workbook: download}) + "/xlsx"
}
