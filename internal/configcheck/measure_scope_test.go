package configcheck

import (
	"github.com/ivantit66/onebase/internal/report"
	"path/filepath"
	"strings"
	"testing"
)

func TestMeasureScopeCheck(t *testing.T) {
	for _, tc := range []struct {
		name  string
		scope []string
		cross bool
		bad   string
	}{
		{name: "grand", scope: []string{"$grand"}},
		{name: "named_case", scope: []string{"м"}},
		{name: "detail", scope: []string{"$detail"}},
		{name: "unknown", scope: []string{"Пропущено"}, bad: "неизвестная область"},
		{name: "blank", scope: []string{""}, bad: "неизвестная область"},
		{name: "duplicate", scope: []string{"М", "м"}, bad: "повторная область"},
		{name: "column_is_not_row_level", scope: []string{"Период"}, cross: true, bad: "неизвестная область"},
		{name: "cross_detail", scope: []string{"$detail"}, cross: true, bad: "несовместима"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &report.Composition{Groupings: []string{"М"}, Measures: []report.Measure{{Field: "Сумма", Agg: "sum", Scope: tc.scope}}}
			if tc.cross {
				c.Columns = []string{"Период"}
			}
			issues := CheckReportComposition(projWith(c))
			if tc.bad == "" && len(issues) != 0 {
				t.Fatal(issues)
			}
			if tc.bad != "" && !issuesContain(issues, tc.bad) {
				t.Fatalf("wanted %q: %+v", tc.bad, issues)
			}
		})
	}
}

func TestMeasureScopeRunFullYAML(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "reports", "scope.yaml"), `name: Scope
query: ВЫБРАТЬ 1 КАК М, 2 КАК Сумма
composition:
  groupings: [М]
  measures:
    - {field: Сумма, agg: sum, scope: ["$grand"]}
  totals: {grand: true, subtotals: true}
variants:
  - name: ПоМ
    composition:
      groupings: [М]
      measures:
        - {field: Сумма, agg: sum, scope: [М]}
`)
	result := RunFullWithOptions(dir, Options{Lint: true})
	if !result.OK {
		t.Fatal(result.Issues)
	}
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "scope") {
			t.Fatalf("scope incorrectly rejected by yaml lint: %+v", w)
		}
	}
}
