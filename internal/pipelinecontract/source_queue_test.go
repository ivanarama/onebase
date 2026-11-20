package pipelinecontract

import (
	"os/exec"
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
		if _, err := exec.LookPath(name); err == nil {
			python = name
			break
		}
	}
	if python == "" {
		t.Skip("Python is unavailable; source queue public CLI suite requires Python 3")
	}
	const scripts = "../../.claude/skills/merge-shepherd/scripts"
	cmd := exec.Command("python3", "-X", "utf8", "-m", "unittest", "discover", "-s", scripts, "-p", "test_source_queue.py", "-v")
	if python == "python" {
		cmd = exec.Command("python", "-X", "utf8", "-m", "unittest", "discover", "-s", scripts, "-p", "test_source_queue.py", "-v")
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("source queue public CLI acceptance: %v\n%s", err, output)
	}
}
