package ui

import (
	"os/exec"
	"testing"
)

func TestManagedDuplicateGridBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the duplicate-grid behavior regression test")
	}
	cmd := exec.Command(node, "--test", "static/managed_duplicate_grid_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node duplicate-grid behavior test: %v\n%s", err, output)
	}
}
