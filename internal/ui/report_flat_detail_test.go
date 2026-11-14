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

func TestReportFlatDetailsHTTP(t *testing.T) {
	rep := &report.Report{Name: "Flat", Query: "SELECT 2 AS Amount UNION ALL SELECT 7 AS Amount",
		Composition: &report.Composition{Detail: true, Measures: []report.Measure{{Field: "Amount", Agg: "sum"}}, Sort: []report.SortKey{{Field: "Amount", Dir: "desc"}}, Totals: report.Totals{Grand: true}}}
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
	body := string(get("/ui/report/Flat?__run=1"))
	if strings.Count(body, `class="det"`) != 2 || strings.Contains(body, `class="grp"`) || !strings.Contains(body, ">7</td>") || !strings.Contains(body, ">2</td>") {
		t.Fatalf("flat screen missing details: %s", body)
	}
	book, err := excelize.OpenReader(bytes.NewReader(get("/ui/report/Flat/excel")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := book.Close(); err != nil {
			t.Error(err)
		}
	}()
	rows, err := book.GetRows(book.GetSheetName(0))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[1][1] != "7" || rows[2][1] != "2" || rows[3][1] != "9" {
		t.Fatalf("flat XLSX: %v", rows)
	}
	if pdf := get("/ui/report/Flat/pdf"); !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatal("invalid PDF")
	}
}
