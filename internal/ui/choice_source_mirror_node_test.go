package ui

import (
	"os"
	"os/exec"
	"testing"
)

// Источник choice_filter, у которого рядом живёт зеркало значения: два
// элемента с одним именем давали пустой источник и пустой зависимый подбор.
func TestChoiceSourceMirrorBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the choice-source mirror regression test")
	}
	cmd := exec.Command(node, "--test", "static/choice_source_mirror_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node choice-source mirror test: %v\n%s", err, output)
	}
}
