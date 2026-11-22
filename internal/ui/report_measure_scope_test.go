package ui

import (
	"bytes"
	"github.com/go-chi/chi/v5"
	"github.com/xuri/excelize/v2"
	"net/http"
	"net/http/httptest"
	"reflect"
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

func TestReportMeasureScopeDuplicateFieldPreset(t *testing.T) {
	grand := reportpkg.Measure{Field: "Days", Agg: "sum", Expr: "17", Scope: []string{reportpkg.MeasureScopeGrand}}
	master := reportpkg.Measure{Field: "Days", Agg: "avg", Expr: "23", Scope: []string{"Master"}}
	for _, tc := range []struct {
		name      string
		requested []reportpkg.Measure
		expected  []reportpkg.Measure
	}{
		{"sum_only", []reportpkg.Measure{{Field: "days", Agg: "sum", Expr: "999", Scope: []string{"Master"}}}, []reportpkg.Measure{grand}},
		{"avg_only", []reportpkg.Measure{{Field: "Days", Agg: "avg"}}, []reportpkg.Measure{master}},
		{"both", []reportpkg.Measure{{Field: "Days", Agg: "sum"}, {Field: "Days", Agg: "avg"}}, []reportpkg.Measure{grand, master}},
		{"reordered", []reportpkg.Measure{{Field: "Days", Agg: "avg"}, {Field: "Days", Agg: "sum"}}, []reportpkg.Measure{master, grand}},
		{"default_sum", []reportpkg.Measure{{Field: "Days"}}, []reportpkg.Measure{grand}},
		{"unknown_agg_preserves_trusted", []reportpkg.Measure{{Field: "Days", Agg: "max"}}, []reportpkg.Measure{grand, master}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := &reportpkg.Composition{Groupings: []string{"Master"}, Columns: []string{"Month"}, Measures: []reportpkg.Measure{grand, master}}
			rep := &reportpkg.Report{Variants: []reportpkg.ReportVariant{{Name: "V", Composition: base}}}
			settings := &reportpkg.UserReportSettings{Variant: "V", Composition: &reportpkg.Composition{Measures: tc.requested}}
			raw, err := settings.JSON()
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := reportpkg.ParseUserSettings(raw)
			if err != nil {
				t.Fatal(err)
			}
			got := effectiveComposition(rep, parsed)
			if len(got.Measures) != len(tc.expected) {
				t.Fatalf("resource count=%d, want%d", len(got.Measures), len(tc.expected))
			}
			for i, want := range tc.expected {
				m := got.Measures[i]
				if m.Field != want.Field || m.Agg != want.Agg || m.Expr != want.Expr || !reflect.DeepEqual(m.Scope, want.Scope) {
					t.Fatalf("resource%d identity/semantics mixed: got%+v want%+v", i, m, want)
				}
			}
		})
	}
}

func TestReportMeasureScopeSameFieldAggIsAmbiguous(t *testing.T) {
	first := reportpkg.Measure{Field: "Days", Agg: "sum", Expr: "17", Scope: []string{reportpkg.MeasureScopeGrand}}
	second := reportpkg.Measure{Field: "Days", Agg: "sum", Expr: "23", Scope: []string{"Master"}}
	base := &reportpkg.Composition{Groupings: []string{"Master"}, Columns: []string{"Month"}, Measures: []reportpkg.Measure{first, second}}
	rep := &reportpkg.Report{Composition: base}
	settings := &reportpkg.UserReportSettings{Composition: &reportpkg.Composition{Measures: []reportpkg.Measure{{Field: "Days", Agg: "sum", Expr: "999", Scope: []string{"Master"}}}}}
	raw, err := settings.JSON()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := reportpkg.ParseUserSettings(raw)
	if err != nil {
		t.Fatal(err)
	}
	got := effectiveComposition(rep, parsed)
	if !reflect.DeepEqual(got.Measures, base.Measures) {
		t.Fatalf("ambiguous preset guessed a resource: got%+v want%+v", got.Measures, base.Measures)
	}
	got.Measures[0].Scope[0] = "changed"
	if base.Measures[0].Scope[0] != reportpkg.MeasureScopeGrand {
		t.Fatal("fallback result mutated trusted metadata")
	}
}
