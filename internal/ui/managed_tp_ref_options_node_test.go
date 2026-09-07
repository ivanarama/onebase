package ui

import (
	"os/exec"
	"testing"
)

func TestManagedTPRefOptionsBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the managed table-part reference-options regression test")
	}
	cmd := exec.Command(node, "--test", "static/managed_tp_ref_options_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node managed table-part reference-options behavior test: %v\n%s", err, output)
	}
}
