package ui

import (
	"os/exec"
	"testing"
)

func TestManagedROMirrorBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the managed readonly mirror behavior regression test")
	}
	cmd := exec.Command(node, "--test", "static/managed_ro_mirror_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node managed ro mirror behavior test: %v\n%s", err, output)
	}
}
