package pipelinecontract

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the actual workflow shell entry point. The real-tool acceptance
// remains lint_runner_driver_test.py; these controls catch a lost CI call,
// broken tool wiring, or a failure hidden by later successful preparation.
func TestCILintRunnerDriverEntryPoint(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable; workflow harness runs in Linux CI")
	}
	workflow := docsWorkflow(t)
	var script string
	var environment map[string]string
	for _, step := range workflow.Jobs["lint"].Steps {
		if step.Name != "Real lint runner driver regression" {
			continue
		}
		if script != "" {
			t.Fatal("real-tool regression must have exactly one workflow entry")
		}
		if step.If != "${{ needs.classify.result != 'success' || needs.classify.outputs.mode != 'docs' }}" {
			t.Fatal("real-tool regression must run for full CI and classifier failure")
		}
		script, environment = step.Run, step.Env
	}
	if script == "" {
		t.Fatal("lint job lost the real-tool driver regression")
	}
	for _, testCase := range []struct {
		name, failure string
		wantDriver    bool
	}{
		{name: "wired tools", wantDriver: true},
		{name: "install failure", failure: "install"},
		{name: "driver failure", failure: "driver", wantDriver: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			if err := os.MkdirAll(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			tools := map[string]string{
				"go": `#!/bin/sh
set -eu
case "$1" in
  install)
    test "$GOTOOLCHAIN" = go1.26.11
    test "$CGO_ENABLED" = 0
    test "$2" = github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4
    test "$#" = 2
    test "$GOBIN" = "$RUNNER_TEMP/lint-runner-tools"
    test "$FAIL_PHASE" != install
    mkdir -p "$GOBIN"
    touch "$GOBIN/golangci-lint"
    ;;
  env)
    test "$GOTOOLCHAIN" = go1.26.3
    test "$2" = GOROOT
    printf '%s\n' "$BOOTSTRAP_ROOT"
    ;;
  *) exit 91 ;;
esac
`,
				"python3": `#!/bin/sh
set -eu
test "$#" = 1
test "$1" = .github/scripts/lint_runner_driver_test.py
test "$LINT_RUNNER_TEST_BOOTSTRAP_GO" = "$BOOTSTRAP_ROOT/bin/go"
test "$LINT_RUNNER_TEST_LINTER" = "$RUNNER_TEMP/lint-runner-tools/golangci-lint"
test -f "$LINT_RUNNER_TEST_LINTER"
printf 'driver reached\n'
test "$FAIL_PHASE" != driver
`,
			}
			for name, body := range tools {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			env := map[string]string{
				"PATH":        bin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"RUNNER_TEMP": root, "BOOTSTRAP_ROOT": filepath.Join(root, "bootstrap"),
				"FAIL_PHASE": testCase.failure,
			}
			for name, value := range environment {
				env[name] = value
			}
			cmd := exec.Command(bash, "-c", script) //nolint:gosec // Actual workflow entry point in a test-owned harness.
			cmd.Dir = root
			cmd.Env = workflowHarnessEnv(env)
			output, runErr := cmd.CombinedOutput()
			if (runErr != nil) != (testCase.failure != "") || strings.Contains(string(output), "driver reached") != testCase.wantDriver {
				t.Fatalf("workflow failure=%q, error=%v, output=%s", testCase.failure, runErr, output)
			}
		})
	}
}
