package ui

import (
	"os/exec"
	"testing"
)

func TestManagedHintFromBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	cmd := exec.Command(node, "--test", "static/managed_hint_from_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node hint_from behavior test: %v\n%s", err, output)
	}
}
