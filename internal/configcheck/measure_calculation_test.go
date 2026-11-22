package configcheck

import (
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/report"
)

func TestMeasureCalculationCheck(t *testing.T) {
	good := report.MeasureCalculation{Level: "M", Agg: "avg", GroupBy: "B", InnerAgg: "avg"}
	for _, tc := range []struct {
		name string
		rule report.MeasureCalculation
		bad  string
	}{
		{"valid", good, ""},
		{"unknown_level", report.MeasureCalculation{Level: "Unknown", Agg: "avg"}, "неизвестный уровень"},
		{"unknown_group", report.MeasureCalculation{Level: "M", Agg: "avg", GroupBy: "Unknown", InnerAgg: "avg"}, "group_by не входит"},
		{"missing_inner", report.MeasureCalculation{Level: "M", Agg: "avg", GroupBy: "B"}, "требует inner_agg"},
		{"unused_inner", report.MeasureCalculation{Level: "M", Agg: "avg", InnerAgg: "avg"}, "требует group_by"},
		{"expr_and_group", report.MeasureCalculation{Level: "M", Agg: "avg", Expr: "1", GroupBy: "B", InnerAgg: "avg"}, "без expr"},
		{"bad_agg", report.MeasureCalculation{Level: "M", Agg: "median"}, "неизвестный агрегат"},
		{"detail", report.MeasureCalculation{Level: "$detail", Agg: "avg"}, "именованный уровень"},
		{"bad_expr", report.MeasureCalculation{Level: "M", Expr: "1+"}, "ошибка формулы"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &report.Composition{Groupings: []string{"M", "B"}, Measures: []report.Measure{{Field: "X", Agg: "avg", Calculations: []report.MeasureCalculation{tc.rule}}}}
			issues := CheckReportComposition(projWith(c))
			if tc.bad == "" && len(issues) != 0 {
				t.Fatal(issues)
			}
			if tc.bad != "" && !issuesContain(issues, tc.bad) {
				t.Fatalf("want %s: %+v", tc.bad, issues)
			}
		})
	}
}
func TestMeasureCalculationRunFullYAML(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "reports", "levels.yaml"), `name: Levels
query: SELECT 1 AS M, 2 AS B, 3 AS X
composition:
  groupings: [M, B]
  measures:
  - field: X
    agg: avg
    calculations:
    - {level: M, agg: avg, group_by: B, inner_agg: avg}
    - {level: $grand, agg: avg, group_by: M, inner_agg: avg}
`)
	got := RunFullWithOptions(dir, Options{Lint: true})
	if !got.OK || len(got.Warnings) > 0 {
		t.Fatalf("%+v", got)
	}
}
