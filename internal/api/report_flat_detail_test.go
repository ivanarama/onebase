package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
)

func TestReportFlatDetailsREST(t *testing.T) {
	rep := &report.Report{Name: "Flat", Query: "SELECT 2 AS Amount UNION ALL SELECT 7 AS Amount", Composition: &report.Composition{
		Detail: true, Measures: []report.Measure{{Field: "Amount", Agg: "sum"}}, Sort: []report.SortKey{{Field: "Amount", Dir: "desc"}}, Totals: report.Totals{Grand: true}}}
	h, _ := newAPITestHandlerWithReports(t, nil, []*report.Report{rep}, nil)
	w := httptest.NewRecorder()
	h.runReportV2().ServeHTTP(w, reqWithEntity("GET", "/api/v2/report/Flat?composition=1", nil, map[string]string{"name": "Flat"}, nil))
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
	if len(got.Data.Result.Details) != 2 || len(got.Data.Result.Groups) != 0 {
		t.Fatalf("REST flat details: %s", w.Body.String())
	}
}
