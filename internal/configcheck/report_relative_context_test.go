package configcheck

import (
	"github.com/ivantit66/onebase/internal/report"
	"testing"
)

func TestReportRelativeContextValidation(t *testing.T) {
	c := &report.Composition{Groupings: []string{"G"}, Columns: []string{"Year"}, Measures: []report.Measure{{Field: "Amount"}, {Field: "X", Expr: "Amount / Grand_Amount + Previous_Amount + First_Amount"}}}
	if issues := CheckReportComposition(projWith(c)); len(issues) != 0 {
		t.Fatal(issues)
	}
	c.Measures[1].Expr = "Grand_Missing"
	if issues := CheckReportComposition(projWith(c)); len(issues) == 0 {
		t.Fatal("unknown contextual measure accepted")
	}
	c.Measures[1].Expr = "Previous_Amount"
	c.Columns = nil
	if issues := CheckReportComposition(projWith(c)); len(issues) == 0 {
		t.Fatal("previous without columns accepted")
	}
}
