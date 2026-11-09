package bootstraptest

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ivantit66/onebase/internal/installtest"
)

func TestPrintedRunShellLiteral(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh targets POSIX systems; install.ps1 is covered by TestBootstrap")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	fixturePath := filepath.Join(t.TempDir(), "release-fixture")
	build := exec.Command("go", "build", "-o", fixturePath, "testdata/fixture.go") //nolint:gosec // G204: fixed fixture source and output beneath t.TempDir.
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, output)
	}
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"CLI space $literal",
		"CLI'quoted'",
		"CLI''consecutive'",
		"CLI `touch injected-backtick`",
		"CLI $(touch injected-substitution)",
		"CLI backslash \\ value",
		"CLI double \"quoted\"",
		"CLI space $literal 'quoted' `touch injected-backtick` $(touch injected-substitution)",
	} {
		t.Run(name, func(t *testing.T) {
			root := installtest.CanonicalTempDir(t)
			installDir := filepath.Join(root, name)
			mkdir(t, installDir)
			executable := filepath.Join(installDir, "onebase")
			write(t, executable, fixture, 0700)
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				http.NotFound(w, r)
			}))
			defer server.Close()
			setup := exec.Command("sh", script, "--install-dir", installDir, "--release-api", server.URL) //nolint:gosec // G204: shipped script and separate fixture arguments only.
			setup.Dir = root
			setup.Env = replaceEnv(os.Environ(), map[string]string{"HOME": root, "literal": "setup-must-not-expand"})
			output, err := setup.CombinedOutput()
			if err != nil {
				t.Fatalf("bootstrap existing CLI: %v\n%s", err, output)
			}
			var command string
			for _, line := range strings.Split(string(output), "\n") {
				if strings.HasPrefix(line, "Run: ") {
					if command != "" {
						t.Fatal("multiple Run commands")
					}
					command = strings.TrimPrefix(line, "Run: ")
				}
			}
			if !strings.HasSuffix(command, " start") {
				t.Fatalf("missing runnable command: %s", output)
			}
			command = strings.TrimSuffix(command, " start") + " --version"
			for _, literal := range []string{"", "must-not-be-expanded"} {
				copied := exec.Command("sh", "-c", command) //nolint:gosec // G204: intentionally execute the shipped Run output with only isolated fixture paths, reproducing copy/paste.
				copied.Dir = root
				copied.Env = replaceEnv(os.Environ(), map[string]string{"HOME": root, "literal": literal})
				got, err := copied.CombinedOutput()
				if err != nil || string(got) != "onebase release-fixture\n" {
					t.Errorf("copied command with literal=%q: %v\n%s\ncommand: %s", literal, err, got, command)
				}
			}
			for _, marker := range []string{"injected-backtick", "injected-substitution"} {
				if _, err := os.Stat(filepath.Join(root, marker)); !os.IsNotExist(err) {
					t.Errorf("copied command executed a path substitution: %s (%v)", marker, err)
				}
			}
			if got, err := os.ReadFile(executable); err != nil || !bytes.Equal(got, fixture) {
				t.Errorf("existing CLI changed: %v", err)
			}
			if requests.Load() != 0 {
				t.Errorf("existing CLI unexpectedly required network: %d", requests.Load())
			}
		})
	}
}
