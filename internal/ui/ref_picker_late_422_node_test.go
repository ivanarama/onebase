package ui

import (
	"os/exec"
	"testing"
)

// Поздний отказ поиска (422) старого запроса окна подбора не стирает результат
// нового: проверка актуальности ответа общая для успеха и для 422.
func TestRefPickerLate422BehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the ref picker late 422 regression test")
	}
	cmd := exec.Command(node, "--test", "static/ref_picker_late_422_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node ref picker late 422 test: %v\n%s", err, output)
	}
}
