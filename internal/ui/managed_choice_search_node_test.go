package ui

import (
	"os/exec"
	"testing"
)

func TestManagedChoiceSearchBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for choice-search editor regression")
	}
	cmd := exec.CommandContext(t.Context(), node, "--test", "static/managed_choice_search_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("choice-search editor: %v\n%s", err, out)
	}
}
