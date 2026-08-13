package ui

import (
	"os/exec"
	"testing"
)

func TestManagedRefCreateBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the ref-editor inline-create regression test")
	}
	cmd := exec.Command(node, "--test", "static/managed_ref_create_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node ref-editor inline-create behavior test: %v\n%s", err, output)
	}
}
