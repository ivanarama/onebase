package ui

import (
	"os/exec"
	"testing"
)

func TestListBasedOnBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the list based-on behavior regression test")
	}
	cmd := exec.Command(node, "--test", "static/list_based_on_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node list based-on behavior test: %v\n%s", err, output)
	}
}
