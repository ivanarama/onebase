package ui

import (
	"os/exec"
	"testing"
)

func TestBusySubmitBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	cmd := exec.Command(node, "--test", "static/busy_submit_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node busy submit behavior test: %v\n%s", err, output)
	}
}
