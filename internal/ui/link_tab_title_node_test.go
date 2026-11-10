package ui

import (
	"os/exec"
	"testing"
)

func TestLinkTabTitleBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the link tab title behavior regression test")
	}
	cmd := exec.Command(node, "--test", "static/link_tab_title_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node link tab title behavior test: %v\n%s", err, output)
	}
}
