package launcher

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func renderCloseDialogBrowserHTML(t *testing.T, policy string) string {
	t.Helper()
	var out bytes.Buffer
	data := map[string]any{
		"Title":        "onebase",
		"Lang":         "ru",
		"Bases":        []*baseVM{},
		"Selected":     (*baseVM)(nil),
		"NativeOK":     false,
		"RunningCount": 0,
		"ClosePolicy":  policy,
	}
	if err := tmpl.ExecuteTemplate(&out, "page-index", data); err != nil {
		t.Fatalf("render page-index: %v", err)
	}
	return out.String()
}

func TestCloseDialogBrowserHTMLAccessibilityAndContract(t *testing.T) {
	html := renderCloseDialogBrowserHTML(t, OnCloseStop)
	for _, want := range []string{
		`id="close-modal"`,
		`role="dialog"`,
		`aria-modal="true"`,
		`aria-labelledby="close-modal-title"`,
		`aria-describedby="close-modal-description"`,
		`id="close-modal-error"`,
		`role="alert"`,
		`id="close-modal-busy"`,
		`aria-live="polite"`,
		`id="close-policy-setting"`,
		`value="ask"`,
		`value="background"`,
		`value="stop"`,
		`max-height:calc(100vh - 40px)`,
		`max-height:180px`,
		`closeRequestJSON('/close-stop'`,
		`var _onebaseCloseDialogBegin = true;`,
		`var _onebaseCloseDialogEnd = true;`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered close dialog does not contain %q", want)
		}
	}
	if strings.Contains(html, "if (_runningCount > 0 && !confirm") {
		t.Error("Stop all confirmation still depends on stale rendered running count")
	}
	start := strings.Index(html, "var _onebaseCloseDialogBegin = true;")
	end := strings.Index(html, "var _onebaseCloseDialogEnd = true;")
	if start < 0 || end <= start {
		t.Fatal("close dialog production JavaScript slice not found")
	}
	closeJS := html[start:end]
	if strings.Contains(closeJS, "fetch('/killall'") {
		t.Error("close flow must use verified /close-stop, not redirecting /killall")
	}
	if !strings.Contains(closeJS, "if (!response.ok)") {
		t.Error("close flow does not reject non-2xx responses")
	}
}

func TestCloseDialogBrowserBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	htmlPath := filepath.Join(t.TempDir(), "launcher-index.html")
	if err := os.WriteFile(htmlPath, []byte(renderCloseDialogBrowserHTML(t, OnCloseAsk)), 0o600); err != nil {
		t.Fatalf("write rendered launcher page: %v", err)
	}
	cmd := exec.Command(node, "--test", "close_dialog_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Env = append(os.Environ(), "ONEBASE_CLOSE_DIALOG_HTML="+htmlPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node close dialog behavior test: %v\n%s", err, output)
	}
}
