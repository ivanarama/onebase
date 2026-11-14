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

func TestReportRelativeContextHTTP(t *testing.T) {
	rep := &report.Report{Name: "Relative", Query: "SELECT 'a' AS G, 2025 AS Year, 80 AS Amount UNION ALL SELECT 'a' AS G, 2024 AS Year, 20 AS Amount", Composition: &report.Composition{Groupings: []string{"G"}, Columns: []string{"Year"}, Measures: []report.Measure{
		{Field: "Amount"}, {Field: "Share", Expr: "Amount / Grand_Amount * 100"}, {Field: "Change", Expr: "Amount - Previous_Amount"}, {Field: "Ratio", Expr: "Amount / First_Amount * 100"},
	}, Totals: report.Totals{Grand: true}}}
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
	body := string(get("/ui/report/Relative?__run=1"))
	if !strings.Contains(body, ">60</td>") || !strings.Contains(body, ">400</td>") {
		t.Fatalf("relative screen: %s", body)
	}
	book, err := excelize.OpenReader(bytes.NewReader(get("/ui/report/Relative/excel")))
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	rows, err := book.GetRows(book.GetSheetName(0))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || len(rows[1]) != 9 || rows[1][2] != "20" || rows[1][3] != "" || rows[1][7] != "60" || rows[1][8] != "400" || rows[2][7] != "60" {
		t.Fatalf("relative XLSX: %v", rows)
	}
	if !bytes.HasPrefix(get("/ui/report/Relative/pdf"), []byte("%PDF")) {
		t.Fatal("invalid PDF")
	}
}
