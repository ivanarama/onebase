package ui

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/xuri/excelize/v2"
)

func TestReportMeasureCalculationPreset(t *testing.T) {
	rule := reportpkg.MeasureCalculation{Level: "D", Agg: "avg", GroupBy: "B", InnerAgg: "avg"}
	base := &reportpkg.Composition{Groupings: []string{"D", "B"}, Measures: []reportpkg.Measure{{Field: "X", Agg: "avg", Calculations: []reportpkg.MeasureCalculation{rule}}}}
	rep := &reportpkg.Report{Variants: []reportpkg.ReportVariant{{Name: "V", Composition: base}}}
	for _, settings := range []*reportpkg.UserReportSettings{
		{Variant: "V"},
		{Variant: "V", Composition: &reportpkg.Composition{Measures: []reportpkg.Measure{{Field: "X", Agg: "sum", Calculations: []reportpkg.MeasureCalculation{{Level: "$grand", Expr: "999"}}}}}},
	} {
		raw, err := settings.JSON()
		if err != nil {
			t.Fatal(err)
		}
		restored, err := reportpkg.ParseUserSettings(raw)
		if err != nil {
			t.Fatal(err)
		}
		got := effectiveComposition(rep, restored)
		if got.Measures[0].Agg != "avg" || !reflect.DeepEqual(got.Measures[0].Calculations, base.Measures[0].Calculations) {
			t.Fatalf("trusted calculation replaced: %+v", got)
		}
		got.Measures[0].Calculations[0].GroupBy = "forged"
		if base.Measures[0].Calculations[0].GroupBy != "B" {
			t.Fatal("trusted calculation mutated")
		}
	}
}

func TestReportMeasureCalculationHTTPExports(t *testing.T) {
	rep, err := reportpkg.ParseBytes([]byte(`name: calculations
query: |
 SELECT "D1" AS Direction,"B1" AS Brigade,100 AS Percentage
 UNION ALL SELECT "D1","B1",100
 UNION ALL SELECT "D1","B1",100
 UNION ALL SELECT "D1","B2",0
 UNION ALL SELECT "D2","B3",100
composition:
 groupings: [Direction, Brigade]
 measures:
 - field: Percentage
   agg: avg
   format: "#,##0.00"
   calculations:
   - {level: Direction, agg: avg, group_by: Brigade, inner_agg: avg}
   - {level: $grand, agg: avg, group_by: Direction, inner_agg: avg}
 totals: {grand: true, subtotals: true}
`))
	if err != nil {
		t.Fatal(err)
	}
	s := newReportExportTestServer(t, rep)
	router := chi.NewRouter()
	s.Mount(router)
	for _, format := range []string{"html", "excel", "pdf"} {
		t.Run(format, func(t *testing.T) {
			path := "/ui/report/calculations?__run=1"
			if format != "html" {
				path = "/ui/report/calculations/" + format
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("HTTP%d %s", w.Code, w.Body.String())
			}
			switch format {
			case "html":
				if !strings.Contains(w.Body.String(), "87,50") || !strings.Contains(w.Body.String(), "50,00") {
					t.Fatal("HTML level/grand formulas lost")
				}
			case "excel":
				book, err := excelize.OpenReader(bytes.NewReader(w.Body.Bytes()))
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
				grand, direction := false, false
				for _, row := range rows {
					for _, value := range row {
						grand = grand || value == "87,50"
						direction = direction || value == "50,00" || value == "50"
					}
				}
				if !grand || !direction {
					t.Fatalf("XLSX formulas lost: %+v", rows)
				}
			case "pdf":
				if !bytes.HasPrefix(w.Body.Bytes(), []byte("%PDF")) {
					t.Fatal("invalid PDF")
				}
			}
		})
	}
	req := reqWithChi(http.MethodGet, "/ui/report/calculations/pdf", nil, map[string]string{"name": "calculations"})
	headers, rows, err := s.reportExportRows(req, rep)
	if err != nil {
		t.Fatal(err)
	}
	doc := buildReportSheet("calculations", headers, rows)
	found := false
	for i, row := range rows {
		if len(row) < 2 {
			continue
		}
		if strings.TrimSpace(fmt.Sprint(row[0])) == "ВСЕГО" {
			if fmtVal(row[1]) != "87,50" {
				t.Fatalf("wrong PDF/common snapshot grand %v", row)
			}
			cell := doc.GetCell(3+i, 1)
			if cell == nil || cell.Text != "87,50" {
				t.Fatal("TabDoc changed grand")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no PDF input grand")
	}
}
