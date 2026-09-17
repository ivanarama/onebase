package launcher

import (
	"os/exec"
	"testing"
)

func TestConfiguratorMultilineBrowser(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for configurator multiline behavior tests")
	}
	cmd := exec.Command(node, "--test", "testdata/configurator_multiline_behavior_test.js") //nolint:gosec // test executable resolved by exec.LookPath
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("multiline browser behavior: %v\n%s", err, out)
	}
}
