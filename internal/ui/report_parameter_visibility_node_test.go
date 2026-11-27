package ui

import (
	"os/exec"
	"testing"
)

func TestReportPeriodVisibilityNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for report parameter visibility")
	}
	cmd := exec.Command(node, "--test", "static/report_params_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("report visibility: %v\n%s", err, output)
	}
}
