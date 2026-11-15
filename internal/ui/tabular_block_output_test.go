package ui

import (
	"bytes"
	"fmt"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/xuri/excelize/v2"
	"net/http"
	"strings"
	"testing"
)

func TestTabularHorizontalBlockHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for index, tc := range []struct {
			name                           string
			leftRows, rightRows, rightCols int
		}{{"KPI_two_by_one", 2, 2, 1}, {"taller_right", 2, 4, 1}, {"shorter_right", 4, 1, 1}, {"repeat_months", 2, 2, 3}} {
			t.Run(tc.name, func(t *testing.T) {
				rep := &reportpkg.Report{Name: fmt.Sprintf("OwnBlock%d", index), ResultProc: "Build"}
				src := fmt.Sprintf(`Функция Build() Д=Новый ТабличныйДокумент;Л=Д.Область(1,1,%d,1);Л.Текст="LEFT";П=Д.Область(1,1,%d,%d);П.Текст="RIGHT";Н=Д.Область(1,1,1,1);Н.Текст="NEXT";Д.Вывести(Л);Д.Присоединить(П);Д.Присоединить(П);Д.Вывести(Н);Возврат Д;КонецФункции`, tc.leftRows, tc.rightRows, tc.rightCols)
				reg := runtime.NewRegistry()
				reg.Load(runtime.LoadOptions{Reports: []*reportpkg.Report{rep}, Programs: map[string]*ast.Program{rep.Name: mustParse(t, src)}})
				i := interpreter.New()
				i.LookupProc = reg.GetModuleProc
				s := &Server{reg: reg, store: db, interp: i, messages: NewMessageStore(), lockMgr: runtime.NewLockManager(), ops: newOperationLimiter()}
				s.entitySvc = s.newEntityService(nil)
				t.Cleanup(s.Close)
				actor := &auth.User{ID: uuid.NewString(), Login: "own-block", Roles: []*auth.Role{{Name: "BlockReport", Permissions: auth.Permission{Reports: map[string][]string{rep.Name: {"run"}}}}}}
				w := tabularHTTP(s, actor, "own-block-session", http.MethodPost, "/ui/report/"+rep.Name, nil)
				if w.Code != 303 {
					t.Fatalf("HTTP%d:%s", w.Code, w.Body.String())
				}
				path := w.Header().Get("Location")
				v := s.tabularResults.items[strings.TrimPrefix(path, "/ui/tabular/")]
				if v == nil {
					t.Fatal("no frozen result")
				}
				bottom := max(tc.leftRows, tc.rightRows)
				right := tc.rightCols * 2
				for _, c := range []int{1, tc.rightCols + 1} {
					cell := v.doc.GetCell(0, c)
					if cell == nil || cell.Text != "RIGHT" {
						t.Fatal("wrong horizontal snapshot coordinate")
					}
				}
				cell := v.doc.GetCell(bottom, 0)
				if cell == nil || cell.Text != "NEXT" {
					t.Fatal("wrong next Put coordinate")
				}
				r, c := v.doc.ContentBounds()
				if r != bottom || c != right {
					t.Fatal("block geometry drift")
				}
				html := tabularHTTP(s, actor, "own-block-session", http.MethodGet, path, nil)
				if html.Code != 200 || !strings.Contains(html.Body.String(), "RIGHT") {
					t.Fatal("viewer missing horizontal output")
				}
				x := tabularHTTP(s, actor, "own-block-session", http.MethodGet, path+"/xlsx", nil)
				f, e := excelize.OpenReader(bytes.NewReader(x.Body.Bytes()))
				if e != nil {
					t.Fatal(e)
				}
				sheet := f.GetSheetList()[0]
				for ref, want := range map[string]string{"B1": "RIGHT", fmt.Sprintf("A%d", bottom+1): "NEXT"} {
					got, e := f.GetCellValue(sheet, ref)
					if e != nil || got != want {
						t.Fatalf("export %s=%q want %q", ref, got, want)
					}
				}
				f.Close()
				pdf := tabularHTTP(s, actor, "own-block-session", http.MethodGet, path+"/pdf", nil)
				if pdf.Code != 200 || !bytes.HasPrefix(pdf.Body.Bytes(), []byte("%PDF-")) {
					t.Fatal("PDF unavailable")
				}
				if len(s.tabularResults.items) != 1 {
					t.Fatal("export reran the module")
				}
			})
		}
	})
}
