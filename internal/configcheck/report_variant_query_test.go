package configcheck

import (
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/report"
	"strings"
	"testing"
)

func TestCheckReportVariantQueries(t *testing.T) {
	for _, bad := range []string{"filters: [{field: X, op: typo, value: 0}]", "filters: [{field: 'X OR 1=1', op: eq, value: 0}]", "filters: [{field: X, op: eq, value: [1, 2]}]", "query: SELECT X FROM РегистрНакопления.Missing.Остатки()"} {
		rep, err := report.ParseBytes([]byte("name: R\nquery: SELECT 1 AS X\nvariants:\n  - name: Broken\n    " + bad + "\n"))
		if err != nil {
			t.Fatal(err)
		}
		issues := CheckQueries(&project.Project{Reports: []*report.Report{rep}})
		if len(issues) == 0 || !strings.Contains(issues[0].Message, "Broken") {
			t.Fatalf("%s: %v", bad, issues)
		}
	}
	rep, err := report.ParseBytes([]byte("name: R\nquery: SELECT 1 AS X\nvariants:\n  - name: Good\n    filters: [{field: X, op: eq, value: 1}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	proj := &project.Project{Reports: []*report.Report{rep}}
	if issues := CheckQueries(proj); len(issues) != 0 {
		t.Fatal(issues)
	}
	calls := 0
	issues := CheckQueriesExecutable(proj, func(sql string) error { calls++; return nil })
	if len(issues) != 0 || calls != 2 {
		t.Fatalf("queries validated=%d issues=%v", calls, issues)
	}
}
