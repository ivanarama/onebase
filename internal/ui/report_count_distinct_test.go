package ui

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/xuri/excelize/v2"
)

func TestReportCountDistinctHTTP(t *testing.T) {
	rep := &report.Report{Name: "Distinct", Query: "SELECT 'a' AS G, 'x' AS ID UNION ALL SELECT 'a' AS G, 'x' AS ID UNION ALL SELECT 'a' AS G, 'y' AS ID UNION ALL SELECT 'b' AS G, 'x' AS ID", Composition: &report.Composition{Groupings: []string{"G"}, Measures: []report.Measure{{Field: "ID", Agg: "count_distinct"}}, Totals: report.Totals{Grand: true}}}
	s := newReportExportTestServer(t, rep)
	router := chi.NewRouter()
	router.Get("/ui/report/{name}", s.reportForm)
	router.Get("/ui/report/{name}/excel", s.reportExcel)
	router.Get("/ui/report/{name}/pdf", s.reportPDF)
	get := func(path string) []byte {
		t.Helper()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	screen := string(get("/ui/report/Distinct?__run=1"))
	if !strings.Contains(screen, ">2</td>") || !strings.Contains(screen, ">1</td>") {
		t.Fatalf("distinct screen: %s", screen)
	}
	book, err := excelize.OpenReader(bytes.NewReader(get("/ui/report/Distinct/excel")))
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	rows, err := book.GetRows(book.GetSheetName(0))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[1][1] != "2" || rows[2][1] != "1" || rows[3][1] != "2" {
		t.Fatalf("distinct XLSX: %v", rows)
	}
	if !bytes.HasPrefix(get("/ui/report/Distinct/pdf"), []byte("%PDF")) {
		t.Fatal("invalid PDF")
	}
}
