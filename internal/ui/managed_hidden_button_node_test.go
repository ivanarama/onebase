package ui

import (
	"os/exec"
	"testing"
)

func TestManagedHiddenButtonBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the managed hidden button behavior regression test")
	}
	cmd := exec.Command(node, "--test", "static/managed_hidden_button_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node managed hidden button behavior test: %v\n%s", err, output)
	}
}
