package ui

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/xuri/excelize/v2"
)

func TestTabularAutoScaleHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, cols := range []int{63, 67} {
			t.Run(fmt.Sprintf("kpi-%d", cols), func(t *testing.T) {
				s, user, rep := tabularFixture(t, db)
				orientation := "Портрет"
				if cols == 67 {
					orientation = "Ландшафт"
				}
				prog := mustParse(t, fmt.Sprintf(`Функция Build()
    Д=Новый ТабличныйДокумент;Д.АвтоМасштаб=Истина;Д.ОриентацияСтраницы="%s";
    Если Не Д.АвтоМасштаб Тогда ВызватьИсключение "print setting missing";КонецЕсли;
    Для К=1 По %d Цикл Д.ШиринаКолонки(К,80);КонецЦикла;
    С=Д.Область(1,1,1,%d);С.Текст="КПИ";
    Для Н=1 По 51 Цикл Д.Вывести(С);КонецЦикла;
    Д.ПовторитьПриПечати(С,Истина);Возврат Д;
   КонецФункции`, orientation, cols, cols))
				s.reg.Load(runtime.LoadOptions{Programs: map[string]*ast.Program{rep.Name: prog}, Reports: []*reportpkg.Report{rep}})
				generated := tabularHTTP(s, user, "own-print-session", "POST", "/ui/report/"+rep.Name, nil)
				if generated.Code != 303 {
					t.Fatalf("generate HTTP%d: %s", generated.Code, generated.Body.String())
				}
				path := generated.Header().Get("Location")
				view := tabularHTTP(s, user, "own-print-session", "GET", path, nil)
				if view.Code != 200 || !strings.Contains(view.Body.String(), `href="`+path+`/print"`) {
					t.Fatal("native print preview link missing")
				}
				rep.ResultProc = "MustNotRunAgain"
				for _, format := range []string{"pdf", "print"} {
					out := tabularHTTP(s, user, "own-print-session", "GET", path+"/"+format, nil)
					if out.Code != 200 || !bytes.HasPrefix(out.Body.Bytes(), []byte("%PDF")) {
						t.Fatalf("native %s failed", format)
					}
					if n := len(regexp.MustCompile(`/Type\s*/Page\b`).FindAll(out.Body.Bytes(), -1)); n != 1 {
						t.Fatalf("scaled synthetic %s pages=%d", format, n)
					}
					disposition := "attachment"
					if format == "print" {
						disposition = "inline"
					}
					if !strings.HasPrefix(out.Header().Get("Content-Disposition"), disposition) {
						t.Fatal("wrong native print disposition")
					}
				}
				denied := tabularHTTP(s, user, "foreign-session", "GET", path+"/print", nil)
				if denied.Code != 404 {
					t.Fatal("foreign print preview allowed")
				}
				xlsx := tabularHTTP(s, user, "own-print-session", "GET", path+"/xlsx", nil)
				if xlsx.Code != 200 {
					t.Fatal("XLSX snapshot export failed")
				}
				book, err := excelize.OpenReader(bytes.NewReader(xlsx.Body.Bytes()))
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := book.Close(); err != nil {
						t.Error(err)
					}
				}()
				layout, err := book.GetPageLayout("Лист1")
				if err != nil {
					t.Fatal(err)
				}
				value, err := book.GetCellValue("Лист1", "A51")
				if err != nil {
					t.Fatal(err)
				}
				if layout.FitToWidth == nil || *layout.FitToWidth != 1 || layout.FitToHeight == nil || *layout.FitToHeight != 0 || value != "КПИ" {
					t.Fatal("XLSX changed values or dropped width-fit")
				}
			})
		}
	})
}
