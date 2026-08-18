package ui

import (
	"os/exec"
	"testing"
)

func TestManagedEnumEditorBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the enum cell editor behavior test")
	}
	cmd := exec.Command(node, "--test", "static/managed_enum_editor_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node enum cell editor behavior test: %v\n%s", err, output)
	}
}
