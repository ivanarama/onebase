package ui

import (
	"os/exec"
	"testing"
)

func TestDateInputBehaviorThroughDOMEvents(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required")
	}
	output, err := exec.Command(node, "static/date_input_behavior_test.js").CombinedOutput() //nolint:gosec // fixed local fixture, node resolved from PATH.
	if err != nil {
		t.Fatalf("date input DOM events: %v\n%s", err, output)
	}
}

func TestReportControlsBehaviorThroughDOMEvents(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required")
	}
	output, err := exec.Command(node, "static/report_controls_behavior_test.js").CombinedOutput() //nolint:gosec // fixed local fixture, node resolved from PATH.
	if err != nil {
		t.Fatalf("report control DOM events: %v\n%s", err, output)
	}
}
