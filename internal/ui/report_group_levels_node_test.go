package ui

import (
	"os/exec"
	"testing"
)

func TestReportGroupLevelsBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for report group levels behavior")
	}
	cmd := exec.Command(node, "static/report_group_levels_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("report group levels: %v\n%s", err, output)
	}
}
