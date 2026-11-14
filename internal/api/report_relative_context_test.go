package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestReportRelativeContextRESTMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		rep := &report.Report{Name: "Relative", Query: "SELECT 'a' AS G, 2025 AS Year, 80 AS Amount UNION ALL SELECT 'a' AS G, 2024 AS Year, 20 AS Amount", Composition: &report.Composition{Groupings: []string{"G"}, Columns: []string{"Year"}, Measures: []report.Measure{{Field: "Amount"}, {Field: "Share", Expr: "Amount / Grand_Amount * 100"}, {Field: "Change", Expr: "Amount - Previous_Amount"}, {Field: "Ratio", Expr: "Amount / First_Amount * 100"}}}}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Reports: []*report.Report{rep}})
		h := &handler{store: db, reg: reg, interp: interpreter.New()}
		router := chi.NewRouter()
		h.mountV2(router)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v2/report/Relative?composition=1", nil))
		if w.Code != 200 {
			t.Fatalf("REST: %d %s", w.Code, w.Body.String())
		}
		var got struct {
			Data struct {
				Result compose.CrossResult `json:"result"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Data.Result.Rows) != 1 || len(got.Data.Result.Warnings) != 0 {
			t.Fatalf("relative REST: %s", w.Body.String())
		}
		found := false
		for _, col := range got.Data.Result.Cols {
			if col.Path[0] == "2025" && col.Measure == "Change" {
				found = true
				d, ok := compose.ExportToDecimal(got.Data.Result.Rows[0].Cells[col.Key()])
				if !ok || d.String() != "60" {
					t.Fatalf("change: %v", got.Data.Result.Rows[0].Cells)
				}
			}
		}
		if !found {
			t.Fatalf("missing contextual column: %s", w.Body.String())
		}
	})
}
