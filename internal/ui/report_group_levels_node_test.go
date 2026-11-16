package ui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
)

func TestReportGroupLevelsBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for report group levels behavior")
	}
	var fixtures []string
	for _, grouped := range []bool{false, true} {
		spec := report.Composition{Groupings: []string{"Key"}, Columns: []string{"Period"}, Measures: []report.Measure{{Field: "Amount", Agg: "sum"}}}
		if grouped {
			spec.Groupings = []string{"Root", "Key"}
		}
		var rows []compose.Row
		for _, key := range []string{"Север", "Я", "▶Север"} {
			rows = append(rows, compose.Row{"Root": "ROOT", "Key": key, "Period": "P1", "Amount": 1})
		}
		result, err := compose.ComposeCross(rows, spec, nil)
		if err != nil {
			t.Fatal(err)
		}
		fixtures = append(fixtures, string(renderCrossTable(result, &spec)))
	}
	data, err := json.Marshal(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	fixturePath := filepath.Join(t.TempDir(), "cross-levels.json")
	if err := os.WriteFile(fixturePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "static/report_group_levels_behavior_test.js", fixturePath) //nolint:gosec // test-only executable resolved by exec.LookPath
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("report group levels: %v\n%s", err, output)
	}
}
