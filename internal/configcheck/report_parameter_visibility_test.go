package configcheck

import (
	"path/filepath"
	"testing"
)

func TestReportVisibilityCheckIsBlocking(t *testing.T) {
	for _, condition := range []string{"{param: Missing, equals: true}", "{param: Flag}", "{param: Text, equals: false}"} {
		dir := t.TempDir()
		mkFile(t, filepath.Join(dir, "reports", "period.yaml"), "name: Period\nquery: ВЫБРАТЬ 1 КАК N\nparams:\n  - {name: Flag, type: bool}\n  - {name: Text, type: string}\n  - name: From\n    type: date\n    visible_when: "+condition+"\n")
		result := RunFull(dir)
		found := false
		for _, issue := range result.Issues {
			if issue.Code == "report.param-visibility" {
				found = true
			}
		}
		if result.OK || !found {
			t.Fatalf("invalid visibility did not block check: %+v", result)
		}
	}
}
