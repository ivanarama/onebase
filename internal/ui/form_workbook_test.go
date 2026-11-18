package ui

import (
	"bytes"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func TestManagedWorkbookDownloadAndUpdateHTTP(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		dir := t.TempDir()
		t.Setenv("ONEBASE_REPORT_XLSX_DIR", dir)
		s, user, _ := tabularFixture(t, db)
		const filename = "Отчёт 400.xlsx"
		form := processorExecutionForm(
			&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Build", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Build"}},
			&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Replace", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Replace"}},
			&metadata.FormElement{Kind: metadata.FormElementButton, Name: "ReplaceAgain", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "ReplaceAgain"}},
		)
		form.ProgramAST = mustParse(t, `Функция MakeBook(Число)
  Лист = Новый ТабличныйДокумент;
  Лист.Ячейка(1,1).Значение = Число;
  Книга = Новый КнигаТабличныхДокументов;
  Книга.ДобавитьЛист("Итого", Лист);
  Возврат Книга;
КонецФункции
Процедура Build()
  СкачатьКнигуExcel("Отчёт 400.xlsx", MakeBook(41));
КонецПроцедуры
Процедура Replace()
  ЗаменитьЛистыExcel("Отчёт 400.xlsx", MakeBook(42));
  Сообщить("Книга обновлена");
КонецПроцедуры
Процедура ReplaceAgain()
  ЗаменитьЛистыExcel("Отчёт 400.xlsx", MakeBook(43));
КонецПроцедуры`)
		proc := &processor.Processor{Name: "WorkbookTest", Forms: []*metadata.FormModule{form}}
		s.reg.LoadProcessors([]*processor.Processor{proc})
		user.Roles[0].Permissions.Processors = map[string][]string{proc.Name: {"run"}}
		base := "/ui/processor/" + proc.Name
		require.Equal(t, http.StatusOK, tabularHTTP(s, user, "own", "GET", base, nil).Code)
		click := func(session, button string) formEventResponse {
			body := url.Values{"_form": {form.Name}, "_element": {button}, "_event": {"Нажатие"}}
			w := tabularHTTP(s, user, session, "POST", base+"/form-event", body)
			require.Equal(t, http.StatusOK, w.Code)
			return decodeFormEventResponse(t, w.Body.Bytes())
		}
		read := func(session string, resp formEventResponse, want string) []byte {
			require.True(t, resp.OK, resp.Error)
			require.Contains(t, resp.WorkbookDownloadURL, "/ui/tabular/")
			require.Equal(t, http.StatusNotFound, tabularHTTP(s, user, "foreign", "GET", resp.WorkbookDownloadURL, nil).Code)
			w := tabularHTTP(s, user, session, "GET", resp.WorkbookDownloadURL, nil)
			require.Equal(t, http.StatusOK, w.Code)
			require.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
			f, err := excelize.OpenReader(bytes.NewReader(w.Body.Bytes()))
			require.NoError(t, err)
			value, err := f.GetCellValue("Итого", "A1")
			require.NoError(t, err)
			require.Equal(t, want, value)
			require.NoError(t, f.Close())
			return w.Body.Bytes()
		}
		missing := click("own", "Replace")
		require.False(t, missing.OK)
		require.Empty(t, missing.WorkbookDownloadURL)
		require.Zero(t, s.tabularResults.reservedBooks)
		first := click("own", "Build")
		newFile := read("own", first, "41")
		path := filepath.Join(dir, filename)
		require.NoFileExists(t, path)
		require.NoError(t, os.WriteFile(path, newFile, 0o600))

		updated := click("second", "Replace")
		require.Contains(t, updated.Messages, "Книга обновлена")
		latest := click("third", "ReplaceAgain")
		read("own", first, "41")
		read("second", updated, "42")
		result := read("third", latest, "43")
		disk, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, result, disk)

		for i := 3; i < maxWorkbookDownloads; i++ {
			next := click("third", "Build")
			require.True(t, next.OK, next.Error)
			require.NotEmpty(t, next.WorkbookDownloadURL)
		}
		full := click("third", "Replace")
		require.False(t, full.OK)
		require.Empty(t, full.WorkbookDownloadURL)
		require.Contains(t, full.Error, "предел выдачи")
		disk, err = os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, result, disk)
		read("own", first, "41")
		read("second", updated, "42")

		store := &s.tabularResults
		store.mu.Lock()
		for _, item := range store.workbooks {
			item.expires = time.Now().Add(-time.Second)
		}
		store.mu.Unlock()
		recovered := click("own", "Replace")
		read("own", recovered, "42")
		require.Equal(t, http.StatusNotFound, tabularHTTP(s, user, "second", "GET", updated.WorkbookDownloadURL, nil).Code)
		user.Roles[0].Permissions.Processors = nil
		require.Equal(t, http.StatusNotFound, tabularHTTP(s, user, "own", "GET", recovered.WorkbookDownloadURL, nil).Code)
	})
}
