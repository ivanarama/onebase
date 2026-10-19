package ui

import (
	"os/exec"
	"testing"
)

func TestGridColumnOrderBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	cmd := exec.Command(node, "--test", "static/grid_column_order_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node grid column order behavior test: %v\n%s", err, output)
	}
}
