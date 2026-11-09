package ui

import (
	"os/exec"
	"testing"
)

func TestChoiceStyleValueBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	cmd := exec.Command(node, "--test", "static/choice_style_value_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node choice_style_value behavior test: %v\n%s", err, output)
	}
}
