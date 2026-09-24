package ui

import (
	"os/exec"
	"testing"
)

func TestQuestionModalBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the question modal behavior regression test")
	}
	cmd := exec.Command(node, "--test", "static/question_modal_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node question modal behavior test: %v\n%s", err, output)
	}
}
