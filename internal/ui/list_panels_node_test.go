package ui

import (
	"os/exec"
	"testing"
)

func TestListPanelsBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the list panels regression test")
	}
	output, err := exec.Command(node, "--test", "static/list_panels_behavior_test.js").CombinedOutput() //nolint:gosec // Test executable resolved by exec.LookPath.
	if err != nil {
		t.Fatalf("list panels behavior: %v\n%s", err, output)
	}
}
