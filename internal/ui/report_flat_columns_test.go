package ui

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/xuri/excelize/v2"
	"golang.org/x/net/html"
)

func assertFlatReportColumns(t *testing.T, body string, want int) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var table *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if table != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "tr" {
			for _, a := range n.Attr {
				if a.Key == "class" && a.Val == "det" {
					for p := n.Parent; p != nil; p = p.Parent {
						if p.Data == "table" {
							table = p
							return
						}
					}
				}
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			find(ch)
		}
	}
	find(doc)
	if table == nil {
		t.Fatal("composed detail table not found")
	}
	rows := 0
	headers := 0
	var check func(*html.Node)
	check = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tr" {
			td, th := 0, 0
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode {
					if c.Data == "td" {
						td++
					}
					if c.Data == "th" {
						th++
					}
				}
			}
			if td > 0 {
				rows++
				if td != want {
					t.Fatalf("HTML row width=%d, want%d", td, want)
				}
			}
			if th > 0 {
				headers++
				if th != want {
					t.Fatalf("HTML header width=%d, want%d", th, want)
				}
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			check(ch)
		}
	}
	check(table)
	if rows == 0 || headers != 1 {
		t.Fatalf("table rows=%d headers=%d", rows, headers)
	}
}

func TestReportFlatColumnsHTTP(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		grouped, link, entity bool
		cols                  int
	}{
		{"flat", false, false, false, 1}, {"flat-drilldown", false, true, true, 2}, {"incomplete-drilldown", false, true, false, 1}, {"grouped", true, false, false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := &report.Composition{Detail: true, Measures: []report.Measure{{Field: "Amount"}}}
			if tc.grouped {
				spec.Groupings = []string{"Category"}
			}
			if tc.link {
				spec.DetailLink = "LinkID"
			}
			if tc.entity {
				spec.DetailEntity = "Synthetic"
			}
			rep := &report.Report{Name: "Columns", Query: "SELECT 'same' AS Category, 'synthetic-id' AS LinkID, 7 AS Amount", Composition: spec}
			s := newReportExportTestServer(t, rep)
			router := chi.NewRouter()
			router.Get("/ui/report/{name}", s.reportForm)
			router.Get("/ui/report/{name}/excel", s.reportExcel)
			get := func(path string) []byte {
				t.Helper()
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
				if w.Code != 200 {
					t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
				}
				return w.Body.Bytes()
			}
			body := string(get("/ui/report/Columns?__run=1"))
			assertFlatReportColumns(t, body, tc.cols)
			if tc.link && tc.entity && !strings.Contains(body, "/ui/document/Synthetic/synthetic-id") {
				t.Fatal("drilldown lost")
			}
			f, err := excelize.OpenReader(bytes.NewReader(get("/ui/report/Columns/excel")))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := f.Close(); err != nil {
					t.Error(err)
				}
			}()
			rows, err := f.GetRows(f.GetSheetName(0))
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if len(row) != tc.cols {
					t.Fatalf("XLSX width=%d want%d: %v", len(row), tc.cols, rows)
				}
			}
			if rows[0][tc.cols-1] != "Amount" || rows[len(rows)-1][tc.cols-1] != "7" {
				t.Fatalf("XLSX shifted values: %v", rows)
			}
		})
	}
}
