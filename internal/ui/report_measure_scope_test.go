package ui

import (
	"bytes"
	"github.com/go-chi/chi/v5"
	"github.com/xuri/excelize/v2"
	"net/http"
	"net/http/httptest"
	"strings"

	reportpkg "github.com/ivantit66/onebase/internal/report"
	"testing"
)

func TestReportMeasureScopeSurvivesUserVariant(t *testing.T) {
	base := &reportpkg.Composition{Groupings: []string{"М"}, Measures: []reportpkg.Measure{{Field: "Сумма", Agg: "sum", Scope: []string{reportpkg.MeasureScopeGrand}}}}
	rep := &reportpkg.Report{Composition: &reportpkg.Composition{}, Variants: []reportpkg.ReportVariant{{Name: "Вариант", Composition: base}}}
	settings := &reportpkg.UserReportSettings{Variant: "Вариант", Composition: &reportpkg.Composition{
		Groupings: []string{"М"}, Measures: []reportpkg.Measure{{Field: "сумма", Agg: "sum", Scope: []string{"М"}}},
	}}
	raw, err := settings.JSON()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := reportpkg.ParseUserSettings(raw)
	if err != nil {
		t.Fatal(err)
	}
	got := effectiveComposition(rep, restored)
	if len(got.Measures) != 1 || !got.Measures[0].InScope(reportpkg.MeasureScopeGrand) || got.Measures[0].InScope("М") {
		t.Fatalf("scope lost or replaced by preset: %+v", got)
	}
	got.Measures[0].Scope[0] = "changed"
	if base.Measures[0].Scope[0] != reportpkg.MeasureScopeGrand {
		t.Fatal("merged settings mutated trusted variant")
	}
}

func TestReportMeasureScopeHTTPExports(t *testing.T) {
	// A distinct number makes an intermediate total observable in real outputs.
	rep := &reportpkg.Report{Name: "scope", Query: "ВЫБРАТЬ 'Б' КАК Бригада, 'М' КАК Мастер, 987654321 КАК Смен, 3 КАК Сумма", Composition: &reportpkg.Composition{
		Groupings: []string{"Бригада", "Мастер"}, Detail: true,
		Measures: []reportpkg.Measure{{Field: "Смен", Agg: "sum", Scope: []string{reportpkg.MeasureScopeGrand}}, {Field: "Сумма", Agg: "sum"}},
		Totals:   reportpkg.Totals{Grand: true, Subtotals: true},
	}}
	s := newReportExportTestServer(t, rep)
	router := chi.NewRouter()
	s.Mount(router)
	for _, format := range []string{"html", "excel", "pdf"} {
		t.Run(format, func(t *testing.T) {
			path := "/ui/report/scope?__run=1"
			if format != "html" {
				path = "/ui/report/scope/" + format
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("HTTP%d: %s", w.Code, w.Body.String())
			}
			switch format {
			case "html":
				if n := strings.Count(w.Body.String(), "987654321"); n != 1 {
					t.Fatalf("grand-only number appeared %d times in HTML", n)
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
				count := 0
				for _, row := range rows {
					for _, value := range row {
						if value == "987654321" {
							count++
							if row[0] != "ВСЕГО" {
								t.Fatalf("resource outside grand: %v", row)
							}
						}
					}
				}
				if count != 1 {
					t.Fatalf("XLSX grand number occurred %d times", count)
				}
			case "pdf":
				if !bytes.HasPrefix(w.Body.Bytes(), []byte("%PDF")) {
					t.Fatal("invalid PDF")
				}
			}
		})
	}
	// reportPDF uses exactly these export rows as buildReportSheet input.
	req := reqWithChi(http.MethodGet, "/ui/report/scope/pdf", nil, map[string]string{"name": "scope"})
	headers, rows, err := s.reportExportRows(req, rep)
	if err != nil {
		t.Fatal(err)
	}
	doc := buildReportSheet("scope", headers, rows)
	count := 0
	for i, row := range rows {
		if row[1] != nil {
			if row[1] != float64(987654321) {
				t.Fatalf("unexpected PDF input value: %v", row[1])
			}
			count++
			if row[0] != "ВСЕГО" {
				t.Fatalf("PDF input resource outside grand: %v", row)
			}
		}
		cell := doc.GetCell(3+i, 1)
		if cell == nil || cell.Text != fmtVal(row[1]) {
			t.Fatal("PDF document changed resource value")
		}
	}
	if count != 1 {
		t.Fatalf("PDF input has %d grand values", count)
	}
}
