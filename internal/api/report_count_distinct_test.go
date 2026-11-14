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

func TestReportCountDistinctRESTMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		rep := &report.Report{Name: "Distinct", Query: "SELECT 'a' AS G, 'x' AS ID UNION ALL SELECT 'a' AS G, 'x' AS ID UNION ALL SELECT 'a' AS G, 'y' AS ID UNION ALL SELECT 'b' AS G, 'x' AS ID", Composition: &report.Composition{Groupings: []string{"G"}, Measures: []report.Measure{{Field: "ID", Agg: "count_distinct"}}, Totals: report.Totals{Grand: true}}}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Reports: []*report.Report{rep}})
		h := &handler{store: db, reg: reg, interp: interpreter.New()}
		router := chi.NewRouter()
		h.mountV2(router)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v2/report/Distinct?composition=1", nil))
		if w.Code != 200 {
			t.Fatalf("REST: %d %s", w.Code, w.Body.String())
		}
		var got struct {
			Data struct {
				Result compose.Result `json:"result"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Data.Result.Grand["ID"] != float64(2) || len(got.Data.Result.Groups) != 2 || got.Data.Result.Groups[0].Subtotals["ID"] != float64(2) {
			t.Fatalf("distinct REST: %s", w.Body.String())
		}
	})
}
