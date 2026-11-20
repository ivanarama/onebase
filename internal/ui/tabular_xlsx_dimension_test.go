package ui

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestTabularXLSXDimensionHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, mode := range []string{"empty", "offset", "merged-span", "styled-tail", "collapsed", "autoscale", "kpi-53893"} {
			t.Run(mode, func(t *testing.T) {
				s, user, rep := tabularFixture(t, db)
				body, want := "", "A1"
				switch mode {
				case "empty":
				case "offset":
					body, want = "Д.Ячейка(830,67).Значение=42;", "A1:BO830"
				case "merged-span":
					body, want = "Д.Ячейка(3,4).Текст=\"merged\";Д.Объединить(3,4,5,8);", "A1:H5"
				case "styled-tail":
					body, want = "Д.Ячейка(1,1).Значение=42;Д.Ячейка(830,67).ЦветФона=\"#fffaf0\";", "A1:BO830"
				case "collapsed":
					body = "С=Д.Область(1,1,1,4);С.Текст=\"42\";Д.НачатьАвтогруппировкуСтрок();Д.Вывести(С,1,\"parent\",Ложь);Д.Вывести(С,2,\"child\",Истина);Д.Вывести(С,3,\"detail\",Истина);Д.ЗакончитьАвтогруппировкуСтрок();"
					want = "A1:D3"
				case "autoscale":
					body, want = "Д.АвтоМасштаб=Истина;Д.Ячейка(51,67).Значение=42;", "A1:BO51"
				case "kpi-53893":
					body = "Г=Д.Область(1,1,18,67);Г.Текст=\"Шапка\";Д.Вывести(Г);Ч=Д.Область(1,1,2,67);\n"
					for col := 1; col <= 36; col++ {
						body += fmt.Sprintf("Ч.R1C%d=\"Шапка\";\n", col)
					}
					body += "Ч.R2C1=\"Шапка\";Д.Вывести(Ч);С=Д.Область(1,1,1,65);С.Текст=\"7\";Для Н=1 По 810 Цикл Д.Вывести(С);КонецЦикла;"
					want = "A1:BO830"
				}
				program := mustParse(t, "Функция Build()\nД=Новый ТабличныйДокумент;\n"+body+"\nВозврат Д;\nКонецФункции")
				s.reg.Load(runtime.LoadOptions{Reports: []*reportpkg.Report{rep}, Programs: map[string]*ast.Program{rep.Name: program}})
				generated := tabularHTTP(s, user, "own-xlsx-dimension", "POST", "/ui/report/"+rep.Name, nil)
				if generated.Code != 303 {
					t.Fatalf("generation HTTP%d: %s", generated.Code, generated.Body.String())
				}
				path := generated.Header().Get("Location")
				snapshot := s.tabularResults.items[strings.TrimPrefix(path, "/ui/tabular/")]
				if snapshot == nil {
					t.Fatal("no snapshot")
				}
				if mode == "kpi-53893" && len(snapshot.doc.Cells) != 53893 {
					t.Fatal("KPI fixture is not complete")
				}
				exported := tabularHTTP(s, user, "own-xlsx-dimension", "GET", path+"/xlsx", nil)
				if exported.Code != 200 {
					t.Fatalf("export HTTP%d", exported.Code)
				}
				z, err := zip.NewReader(bytes.NewReader(exported.Body.Bytes()), int64(exported.Body.Len()))
				if err != nil {
					t.Fatal(err)
				}
				var worksheet struct {
					Dimension struct {
						Ref string `xml:"ref,attr"`
					} `xml:"dimension"`
					Rows []struct {
						Collapsed bool `xml:"collapsed,attr"`
						Cells     []struct {
							Ref string `xml:"r,attr"`
						} `xml:"c"`
					} `xml:"sheetData>row"`
					PageSetup struct {
						Width  string `xml:"fitToWidth,attr"`
						Height string `xml:"fitToHeight,attr"`
					} `xml:"pageSetup"`
				}
				found := false
				for _, entry := range z.File {
					if entry.Name != "xl/worksheets/sheet1.xml" {
						continue
					}
					r, err := entry.Open()
					if err != nil {
						t.Fatal(err)
					}
					err = xml.NewDecoder(r).Decode(&worksheet)
					closeErr := r.Close()
					if err != nil {
						t.Fatal(err)
					}
					if closeErr != nil {
						t.Fatal(closeErr)
					}
					found = true
				}
				if !found || worksheet.Dimension.Ref != want {
					t.Fatalf("dimension=%q, want %q", worksheet.Dimension.Ref, want)
				}
				cells, collapsed := 0, false
				for _, row := range worksheet.Rows {
					cells += len(row.Cells)
					collapsed = collapsed || row.Collapsed
				}
				if mode == "kpi-53893" && cells != 53893 {
					t.Fatalf("KPI XML cells=%d", cells)
				}
				if mode == "collapsed" && !collapsed {
					t.Fatal("group collapse metadata changed")
				}
				if mode == "autoscale" && (worksheet.PageSetup.Width != "1" || worksheet.PageSetup.Height != "0") {
					t.Fatal("autoscale print settings changed")
				}
				view := tabularHTTP(s, user, "own-xlsx-dimension", "GET", path, nil)
				if view.Code != 200 {
					t.Fatal("export invalidated snapshot")
				}
			})
		}
	})
}
