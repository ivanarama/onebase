package launcher

import (
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compform"
	"net/url"
	"strings"
	"testing"
)

func TestReportMeasureScopeEditorRoundTrip(t *testing.T) {
	comp := &report.Composition{Groupings: []string{"М"}, Measures: []report.Measure{{Field: "Сумма", Agg: "sum", Scope: []string{report.MeasureScopeGrand, "М"}}}}
	page := renderConfiguratorReport(t, comp)
	if !strings.Contains(page, `name="comp.measure.0.scope" value="$grand, М"`) {
		t.Fatal("scope not rendered in editor")
	}
	form := url.Values{"comp.present": {"1"}, "comp.grouping.0": {"М"}, "comp.measure.0.field": {"Сумма"}, "comp.measure.0.agg": {"sum"}, "comp.measure.0.scope": {" $grand , М "}}
	parsed, present := compform.Parse(form)
	if !present || parsed == nil || len(parsed.Measures) != 1 || len(parsed.Measures[0].Scope) != 2 || !parsed.Measures[0].InScope("м") || !parsed.Measures[0].InScope(report.MeasureScopeGrand) {
		t.Fatalf("scope editor roundtrip failed: %+v", parsed)
	}
}
