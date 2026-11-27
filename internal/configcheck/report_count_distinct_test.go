package configcheck

import (
	"github.com/ivantit66/onebase/internal/report"
	"testing"
)

func TestReportCountDistinctValidation(t *testing.T) {
	c := &report.Composition{Groupings: []string{"G"}, Measures: []report.Measure{{Field: "ID", Agg: "count_distinct"}}}
	if issues := CheckReportComposition(projWith(c)); len(issues) != 0 {
		t.Fatalf("count_distinct: %v", issues)
	}
}
