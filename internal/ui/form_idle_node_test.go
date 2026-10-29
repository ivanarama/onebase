package ui

import (
	"os"
	"os/exec"
	"testing"
)

func TestFormIdleTimerBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the form idle timer test")
	}
	cmd := exec.Command(node, "--test", "static/form_idle_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node form idle test: %v\n%s", err, output)
	}
}
