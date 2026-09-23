package ui

import (
	"os"
	"os/exec"
	"testing"
)

func TestChoiceFilterRefreshBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the choice_filter refresh regression test")
	}
	cmd := exec.Command(node, "--test", "static/choice_filter_refresh_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node choice_filter refresh test: %v\n%s", err, output)
	}
}
