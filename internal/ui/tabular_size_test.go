package ui

import (
	"fmt"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// These are public report-generation/viewer requests, not a private publish call.
// The synthetic areas preserve the KPI shape/count, without copying business data.
func TestTabularSizeHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, mode := range []string{"control-1308", "kpi-53893", "max-100000", "over-cells", "over-rectangle", "over-bytes", "combined-payload-bytes", "over-rows", "over-columns", "over-header"} {
			t.Run(mode, func(t *testing.T) {
				s, user, rep := tabularFixture(t, db)
				body := ""
				wantCells := 0
				switch mode {
				case "control-1308", "kpi-53893":
					rows := 1
					if mode == "kpi-53893" {
						rows = 810
					}
					// 18*67 + 37 = 1243 header cells, then rows*65 details.
					body = "Г=Д.Область(1,1,18,67);Г.Текст=\"Шапка\";Д.Вывести(Г);Ч=Д.Область(1,1,2,67);\n"
					for col := 1; col <= 36; col++ {
						body += fmt.Sprintf("Ч.R1C%d=\"Шапка\";\n", col)
					}
					body += "Ч.R2C1=\"Шапка\";Д.Вывести(Ч);С=Д.Область(1,1,1,65);С.Текст=\"7\";\n"
					body += fmt.Sprintf("Для Н=1 По %d Цикл Д.Вывести(С);КонецЦикла;", rows)
					wantCells = 1243 + rows*65
				case "max-100000":
					body = "С=Д.Область(1,1,1,100);С.Текст=\"7\";Для Н=1 По 1000 Цикл Д.Вывести(С);КонецЦикла;"
					wantCells = 100000
				case "over-cells":
					body = "С=Д.Область(1,1,1,100);С.Текст=\"7\";Для Н=1 По 1000 Цикл Д.Вывести(С);КонецЦикла;Д.Ячейка(1001,1).Текст=\"extra\";"
				case "over-rectangle":
					body = "Д.Ячейка(1001,100).Текст=\"sparse\";"
				case "over-bytes":
					body = "Т=\"x\";Для Н=1 По 23 Цикл Т=Т+Т;КонецЦикла;Д.Ячейка(1,1).Текст=Т+\"x\";"
				case "combined-payload-bytes":
					body = "Т=\"x\";Для Н=1 По 20 Цикл Т=Т+Т;КонецЦикла;Д.Ячейка(1,1).Текст=Т+Т+Т+Т+Т+Т+Т+Лев(Т,524288);Д.Ячейка(1,1).Расшифровка=Лев(Т,786432);"
				case "over-rows":
					body = "Д.Ячейка(21000,1).Текст=\"sparse\";"
				case "over-columns":
					body = "Д.Ячейка(1,514).Текст=\"sparse\";"
				case "over-header":
					body = "Д.Ячейка(1,1).Текст=\"ok\";Ш=Д.Область(1,1,21000,1);Д.ПовторитьПриПечати(Ш,Истина);"
				}
				prog := mustParse(t, "Функция Build()\nД=Новый ТабличныйДокумент;\n"+body+"\nВозврат Д;\nКонецФункции")
				s.reg.Load(runtime.LoadOptions{Programs: map[string]*ast.Program{rep.Name: prog}, Reports: []*reportpkg.Report{rep}})
				var before, after goruntime.MemStats
				goruntime.ReadMemStats(&before)
				start := time.Now()
				w := tabularHTTP(s, user, "own-size-session", "POST", "/ui/report/"+rep.Name, nil)
				if wantCells == 0 {
					if w.Code == 303 || len(s.tabularResults.items) != 0 {
						t.Fatal("over-budget document published")
					}
					if !strings.Contains(w.Body.String(), "превышает") && !strings.Contains(w.Body.String(), "повторяемая область") {
						t.Fatalf("expected budget rejection, HTTP%d: %s", w.Code, w.Body.String())
					}
					return
				}
				if w.Code != 303 {
					t.Fatalf("size %s generation HTTP%d: %s", mode, w.Code, w.Body.String())
				}
				path := w.Header().Get("Location")
				snapshot := s.tabularResults.items[strings.TrimPrefix(path, "/ui/tabular/")]
				if snapshot == nil || len(snapshot.doc.Cells) != wantCells {
					t.Fatal("cells truncated or missing")
				}
				if mode == "kpi-53893" {
					r, c := snapshot.doc.ContentBounds()
					if r != 829 || c != 66 {
						t.Fatalf("KPI shape %dx%d", r+1, c+1)
					}
				}
				view := tabularHTTP(s, user, "own-size-session", "GET", path, nil)
				if view.Code != 200 {
					t.Fatal("accepted size has no native viewer")
				}
				goruntime.ReadMemStats(&after)
				t.Logf("cells=%d viewer_bytes=%d generation_and_view_ms=%d total_alloc_bytes=%d", wantCells, view.Body.Len(), time.Since(start).Milliseconds(), after.TotalAlloc-before.TotalAlloc)
			})
		}
	})
}
