package ui

import (
	"os/exec"
	"testing"
)

func TestWidgetRefreshBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the widget refresh behavior regression test")
	}
	cmd := exec.Command(node, "--test", "static/widget_refresh_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node widget refresh behavior test: %v\n%s", err, output)
	}
}
