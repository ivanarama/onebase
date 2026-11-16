package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/userdata"
)

func TestPortableStartupSelectsWebviewProfileBeforeCobra(t *testing.T) {
	root := t.TempDir()
	t.Setenv(userdata.PortableRootEnv, root)
	t.Setenv("ONEBASE_WEBVIEW_PROFILE", "")
	t.Chdir(t.TempDir())
	args, oldNoGUI := os.Args, noGUI
	t.Cleanup(func() { os.Args, noGUI = args, oldNoGUI })
	os.Args = []string{"onebase", "--no-gui"}
	if !PrepareUserData() || !guiDisabled() {
		t.Fatal("startup preparation failed or ignored --no-gui")
	}
	if got := os.Getenv("ONEBASE_WEBVIEW_PROFILE"); got != filepath.Join(root, ".onebase", "browser-profiles", "shared") {
		t.Fatalf("WebView profile=%s", got)
	}
	t.Setenv("ONEBASE_WEBVIEW_PROFILE", filepath.Join(root, "explicit-profile"))
	if !PrepareUserData() || os.Getenv("ONEBASE_WEBVIEW_PROFILE") != filepath.Join(root, "explicit-profile") {
		t.Fatal("explicit isolated profile was overwritten")
	}
}
