package ui

import (
	"os/exec"
	"testing"
)

func TestDevSystemBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the dev-system behavior regression test")
	}
	cmd := exec.Command(node, "--test", "static/dev_system_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node dev-system behavior test: %v\n%s", err, output)
	}
}
