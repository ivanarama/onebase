package ui

import (
	"os"
	"os/exec"
	"testing"
)

func TestRefPickerPreviewFailClosedBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the reference picker preview regression test")
	}
	cmd := exec.Command(node, "--test", "static/ref_picker_preview_failclosed_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node reference picker preview test: %v\n%s", err, output)
	}
}
