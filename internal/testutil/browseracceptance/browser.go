// Package browseracceptance runs the opt-in public browser fixtures used by e2e CI.
package browseracceptance

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func Require(t *testing.T) {
	t.Helper()
	if os.Getenv("ONEBASE_BROWSER_ACCEPTANCE") != "1" {
		t.Skip("set ONEBASE_BROWSER_ACCEPTANCE=1 after npm ci and playwright install in e2e/; required by e2e CI")
	}
}

func Root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
}

func Run(t *testing.T, suite string, env map[string]string) {
	t.Helper()
	Require(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var cmd *exec.Cmd
	switch suite {
	case "runtime":
		cmd = exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "--grep", "runtime")
	case "configurator":
		cmd = exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "--grep", "configurator")
	default:
		t.Fatalf("unknown browser acceptance suite %q", suite)
	}
	cmd.Dir = filepath.Join(Root(), "e2e")
	cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE="+suite)
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.CombinedOutput()
	t.Logf("%s", output)
	if err != nil {
		t.Fatalf("browser acceptance %s: %v", suite, err)
	}
}
