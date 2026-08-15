package ui

import (
	"os/exec"
	"testing"
)

// Маска ввода живёт в браузере, поэтому её поведение проверяется node'ом на
// настоящем коде managed.js (тот же приём, что у теста дублирующегося грида).
func TestManagedInputMaskBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("для проверки маски ввода нужен node")
	}
	cmd := exec.Command(node, "--test", "static/managed_input_mask_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node input-mask behavior test: %v\n%s", err, output)
	}
}
