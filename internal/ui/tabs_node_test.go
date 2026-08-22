package ui

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestTabsBehaviorInNode executes the real app-shell script in a deterministic
// DOM. Presence-only assertions cannot detect hydration overwriting the saved
// active tab or duplicate URLs collapsing into one tab.
func TestTabsBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the tabs behavior regression test")
	}

	data := map[string]any{
		"Cfg":              Config{AppName: "Test"},
		"Lang":             "ru",
		"Subsystems":       []any{},
		"CurrentSubsystem": "",
		"Nav":              []any{},
		"CollapsibleNav":   false,
		"IsAdmin":          false,
	}
	var rendered bytes.Buffer
	if err := tmpl.ExecuteTemplate(&rendered, "page-app-shell", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	htmlPath := filepath.Join(t.TempDir(), "app-shell.html")
	if err := os.WriteFile(htmlPath, rendered.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(node, "--test", "static/tabs_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Env = append(os.Environ(), "ONEBASE_TABS_HTML="+htmlPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node tabs behavior test: %v\n%s", err, output)
	}
}
