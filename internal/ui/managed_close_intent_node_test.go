package ui

import (
	"os/exec"
	"testing"
)

func TestManagedCloseIntentBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the managed close-intent behavior test")
	}
	cmd := exec.Command(node, "--test", "static/managed_close_intent_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node managed close-intent behavior test: %v\n%s", err, output)
	}
}

func TestFormCloseBridgeBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the form close bridge behavior test")
	}
	cmd := exec.Command(node, "--test", "static/form_close_bridge_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node form close bridge behavior test: %v\n%s", err, output)
	}
}

func TestAutoFormDirtyBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the auto-form dirty behavior test")
	}
	cmd := exec.Command(node, "--test", "static/auto_form_dirty_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node auto-form dirty behavior test: %v\n%s", err, output)
	}
}
