package ui

import (
	"os/exec"
	"testing"
)

func TestTPChoiceBrowserBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for TP choice behavior")
	}
	cmd := exec.Command(node, "--test", "static/tp_choice_filter_behavior_test.js") //nolint:gosec // test executable resolved with LookPath
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("TP choice browser behavior: %v\n%s", err, out)
	}
}
