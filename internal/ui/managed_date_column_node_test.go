package ui

import (
	"os/exec"
	"testing"
)

func TestManagedDateColumnBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the date cell column behavior test")
	}
	cmd := exec.Command(node, "--test", "static/managed_date_column_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node date cell column behavior test: %v\n%s", err, output)
	}
}
