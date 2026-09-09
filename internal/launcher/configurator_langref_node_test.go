package launcher

import (
	"os/exec"
	"testing"
)

func TestConfiguratorJS_LangrefReceiverBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the langref receiver behavior test")
	}
	cmd := exec.Command(node, "--test", "testdata/configurator_langref_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node langref receiver behavior test: %v\n%s", err, out)
	}
}
