package ui

import (
	"os/exec"
	"testing"
)

func TestWidgetRowNavBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the widget row navigation behavior regression test")
	}
	cmd := exec.Command(node, "--test", "static/widget_row_nav_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node widget row nav behavior test: %v\n%s", err, output)
	}
}
