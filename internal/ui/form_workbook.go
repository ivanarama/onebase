package ui

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/sheet"
)

type workbookDownload struct {
	name string
	data []byte
}

const (
	maxWorkbookDownloads     = 16
	maxWorkbookDownloadBytes = 4 * sheet.MaxUpdateXLSXBytes
)

type workbookReservation struct {
	store      *tabularResultStore
	owner      [32]byte
	generation uint64
	download   *workbookDownload
}

func (s *Server) reserveWorkbook(owner [32]byte) (*workbookReservation, error) {
	store := &s.tabularResults
	store.mu.Lock()
	defer store.mu.Unlock()
	now := time.Now()
	generation := s.reg.Generation()
	for token, result := range store.workbooks {
		if !now.Before(result.expires) || result.generation != generation {
			store.workbookBytes -= len(result.workbook.data)
			delete(store.workbooks, token)
		}
	}
	if len(store.workbooks)+store.reservedBooks >= maxWorkbookDownloads ||
		store.workbookBytes+(store.reservedBooks+1)*sheet.MaxUpdateXLSXBytes > maxWorkbookDownloadBytes {
		return nil, fmt.Errorf("достигнут предел выдачи книг XLSX, повторите позднее")
	}
	// Reserve the largest possible result before changing the server file.
	store.reservedBooks++
	return &workbookReservation{store: store, owner: owner, generation: generation}, nil
}

func (reservation *workbookReservation) release() {
	if reservation == nil || reservation.store == nil {
		return
	}
	store := reservation.store
	store.mu.Lock()
	store.reservedBooks--
	store.mu.Unlock()
	reservation.store = nil
}

func (s *Server) bindWorkbookCommands(ctx context.Context, w http.ResponseWriter, r *http.Request, vars map[string]any, output **workbookReservation) {
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
			owner, err := ensureTabularOwner(w, r)
			if err != nil {
				return nil, err
			}
			reservation, err := s.reserveWorkbook(owner)
			if err != nil {
				return nil, err
			}
			defer func() {
				if *output != reservation {
					reservation.release()
				}
			}()
			var data []byte
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
			reservation.download = &workbookDownload{name: name, data: data}
			*output = reservation
			return true, nil
		}
	}
	replace, create := makeCommand(true), makeCommand(false)
	vars["ЗаменитьЛистыExcel"], vars["ReplaceExcelSheets"] = replace, replace
	vars["СкачатьКнигуExcel"], vars["DownloadExcelWorkbook"] = create, create
}

func attachWorkbook(scope tabularScope, reservation *workbookReservation, resp *formEventResponse) {
	if reservation == nil {
		return
	}
	store := reservation.store
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.workbooks == nil {
		store.workbooks = make(map[string]*tabularResult)
	}
	token := uuid.NewString()
	store.workbooks[token] = &tabularResult{
		scope: scope, owner: reservation.owner, workbook: reservation.download,
		expires: time.Now().Add(15 * time.Minute), generation: reservation.generation,
	}
	store.workbookBytes += len(reservation.download.data)
	store.reservedBooks--
	reservation.store = nil
	resp.WorkbookDownloadURL = "/ui/tabular/" + token + "/xlsx"
}
