package ui

import (
	"os/exec"
	"testing"
)

func TestRowURLBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the row-url behavior regression test")
	}
	cmd := exec.Command(node, "--test", "static/row_url_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node row-url behavior test: %v\n%s", err, output)
	}
}
