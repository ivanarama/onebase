package launcher

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStartErrorRenumberBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	htmlPath := filepath.Join(t.TempDir(), "launcher-index.html")
	if err := os.WriteFile(htmlPath, []byte(startErrorPage(t)), 0o600); err != nil {
		t.Fatalf("write rendered launcher page: %v", err)
	}
	cmd := exec.Command(node, "--test", "start_error_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Env = append(os.Environ(), "ONEBASE_START_ERROR_HTML="+htmlPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node start-error behavior test: %v\n%s", err, output)
	}
}

func TestMinimumEngineVersionLaunchNavigationInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	htmlPath := filepath.Join(t.TempDir(), "launcher-index.html")
	if err := os.WriteFile(htmlPath, []byte(startErrorPage(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--test", "min_engine_version_behavior_test.js") //nolint:gosec // fixed test script
	cmd.Env = append(os.Environ(), "ONEBASE_START_ERROR_HTML="+htmlPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("launch navigation: %v\n%s", err, out)
	}
}
