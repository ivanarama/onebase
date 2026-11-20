package ui

import (
	"os/exec"
	"testing"
)

func TestGridDeclaredWidthsBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the declared column width behavior regression test")
	}
	cmd := exec.Command(node, "--test", "static/grid_declared_widths_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node declared column width behavior test: %v\n%s", err, output)
	}
}
