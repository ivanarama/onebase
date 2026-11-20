package ui

import (
	"os/exec"
	"testing"
)

func TestTreeFilterJavaScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the tree filter behavior test")
	}
	cmd := exec.CommandContext(t.Context(), node, "--test", "static/tree_filter_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tree filter behavior: %v\n%s", err, output)
	}
}
