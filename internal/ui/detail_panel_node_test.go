package ui

import (
	"os/exec"
	"testing"
)

func TestDetailPanelBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the detail-panel behavior regression test")
	}
	cmd := exec.Command(node, "--test", "static/detail_panel_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node detail-panel behavior test: %v\n%s", err, output)
	}
}
