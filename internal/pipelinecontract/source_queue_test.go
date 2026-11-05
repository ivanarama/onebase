package pipelinecontract

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestTechnicalSourceQueueCLI(t *testing.T) {
	pythonNames := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		pythonNames = []string{"python", "python3"}
	}
	var python string
	for _, name := range pythonNames {
		if path, err := exec.LookPath(name); err == nil {
			python = path
			break
		}
	}
	if python == "" {
		t.Skip("Python is unavailable; source queue public CLI suite requires Python 3")
	}
	scripts := filepath.Join("..", "..", ".claude", "skills", "merge-shepherd", "scripts")
	cmd := exec.Command(python, "-X", "utf8", "-m", "unittest", "discover", "-s", scripts, "-p", "test_source_queue.py", "-v")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("source queue public CLI acceptance: %v\n%s", err, output)
	}
}
