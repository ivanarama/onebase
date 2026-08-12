package launcher

import (
	"os/exec"
	"testing"
)

func TestConfiguratorJS_BreakpointConditionRollbackBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the breakpoint rollback behavior test")
	}
	cmd := exec.Command(node, "--test", "testdata/configurator_breakpoint_condition_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node breakpoint rollback behavior test: %v\n%s", err, out)
	}
}
