//go:build windows

package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise the public CLI error path in fresh processes: a regression opening
// MessageBox on redirected stderr would hang until the strict deadline.
func TestWindowsStartupErrorsThroughCLI(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, mode string }{
		{"pipe without GUI opt-out", "pipe"},
		{"file without GUI opt-out", "file"},
		{"no-gui flag with unavailable stderr", "flag"},
		{"no-gui environment with unavailable stderr", "environment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			//nolint:gosec // G204: current test executable, fixed helper selector; no shell.
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestWindowsStartupErrorCLIHelper$")
			for _, item := range os.Environ() {
				if !strings.HasPrefix(strings.ToUpper(item), "ONEBASE_NO_GUI=") && !strings.HasPrefix(item, "ONEBASE_ERROR_CLI_HELPER=") {
					cmd.Env = append(cmd.Env, item)
				}
			}
			cmd.Env = append(cmd.Env, "ONEBASE_ERROR_CLI_HELPER="+tc.mode)
			if tc.mode == "environment" {
				cmd.Env = append(cmd.Env, "ONEBASE_NO_GUI=1")
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			path := filepath.Join(t.TempDir(), "stderr.txt")
			if tc.mode == "file" {
				file, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = file.Close() }()
				cmd.Stderr = file
			}
			err := cmd.Run()
			if ctx.Err() != nil {
				t.Fatal("CLI opened a modal error dialog instead of exiting")
			}
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
				t.Fatalf("want exit1, got %v: %s", err, stderr.String())
			}
			if tc.mode == "file" {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := stderr.Write(data); err != nil {
					t.Fatal(err)
				}
			}
			if tc.mode == "pipe" || tc.mode == "file" {
				if !strings.Contains(stderr.String(), "unknown flag") {
					t.Fatalf("missing CLI error: %s", stderr.String())
				}
			}
		})
	}
}

func TestWindowsStartupErrorCLIHelper(t *testing.T) {
	mode := os.Getenv("ONEBASE_ERROR_CLI_HELPER")
	if mode == "" {
		return
	}
	os.Args = []string{"onebase"}
	if mode == "flag" {
		os.Args = append(os.Args, "--no-gui")
	}
	if mode == "flag" || mode == "environment" {
		os.Stderr = os.NewFile(^uintptr(0), "unavailable")
	}
	os.Args = append(os.Args, "--onebase-invalid-flag-for-startup-error")
	Execute()
	os.Exit(2)
}
