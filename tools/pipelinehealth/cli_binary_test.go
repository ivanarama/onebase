package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// Cache compiled executables only for this test process. Each CLI scenario
// still executes a fresh subprocess with its own fixtures, environment and
// assertions; no command output, verdict or test result is reused.
var cliBinaries struct {
	sync.Mutex
	directory string
	paths     map[string]string
}

func pipelinehealthCLI(t *testing.T) string {
	t.Helper()
	return testCLIBinary(t, "pipelinehealth")
}

func testCLIBinary(t *testing.T, name string) string {
	t.Helper()
	cliBinaries.Lock()
	defer cliBinaries.Unlock()
	if path := cliBinaries.paths[name]; path != "" {
		return path
	}
	packages := map[string]string{"pipelinehealth": "./tools/pipelinehealth", "ghstub": "./tools/pipelinehealth/internal/ghstub"}
	pkg, ok := packages[name]
	if !ok {
		t.Fatalf("unknown test executable %q", name)
	}
	if cliBinaries.directory == "" {
		var err error
		cliBinaries.directory, err = os.MkdirTemp("", "pipelinehealth-cli-tests-")
		if err != nil {
			t.Fatal(err)
		}
		cliBinaries.paths = make(map[string]string)
	}
	path := filepath.Join(cliBinaries.directory, name+".exe")
	//nolint:gosec // Fixed package whitelist and process-owned temporary output.
	command := exec.Command("go", "build", "-o", path, pkg)
	command.Dir = repoRoot(t)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, output)
	}
	cliBinaries.paths[name] = path
	return path
}

func cleanupCLIBinaries() {
	if cliBinaries.directory != "" {
		if err := os.RemoveAll(cliBinaries.directory); err != nil {
			fmt.Fprintln(os.Stderr, "remove test binaries:", err)
		}
	}
}
