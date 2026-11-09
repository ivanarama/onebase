package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/ivantit66/onebase/internal/userdata"
)

func TestPortableStartupRejectsWebviewProfileOverflow(t *testing.T) {
	for _, tc := range []struct {
		name     string
		units    int
		explicit bool
		suffix   string
		wantOK   bool
	}{
		{name: "shared fits", units: 259, wantOK: true},
		{name: "shared overflows", units: 260},
		{name: "explicit fits", units: 259, explicit: true, wantOK: true},
		{name: "explicit overflows", units: 260, explicit: true},
		{name: "surrogate pair fits", units: 259, explicit: true, suffix: "😀", wantOK: true},
		{name: "surrogate pair overflows", units: 260, explicit: true, suffix: "😀"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			profile := ""
			if tc.explicit {
				prefix := filepath.Join(root, "profile")
				padding := tc.units - len(utf16.Encode([]rune(prefix+tc.suffix)))
				if padding < 0 {
					t.Fatalf("temporary path too long for boundary test: %s", root)
				}
				profile = prefix + strings.Repeat("x", padding) + tc.suffix
			} else {
				// Keep the bundle itself below MAX_PATH, while its generated
				// .onebase/browser-profiles/shared path reaches the boundary.
				prefix := filepath.Join(root, "bundle")
				padding := tc.units - len(utf16.Encode([]rune(filepath.Join(prefix, ".onebase", "browser-profiles", "shared"))))
				if padding < 0 {
					t.Fatalf("temporary path too long for boundary test: %s", root)
				}
				root = prefix + strings.Repeat("x", padding)
				if err := os.MkdirAll(root, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv(userdata.PortableRootEnv, root)
			t.Setenv("ONEBASE_WEBVIEW_PROFILE", profile)
			t.Chdir(t.TempDir())
			args, oldNoGUI := os.Args, noGUI
			t.Cleanup(func() { os.Args, noGUI = args, oldNoGUI })
			os.Args = []string{"onebase", "--no-gui"}
			if got := PrepareUserData(); got != tc.wantOK {
				t.Fatalf("PrepareUserData()=%v, want %v for %d UTF-16 units", got, tc.wantOK, tc.units)
			}
			wantProfile := profile
			if tc.wantOK && profile == "" {
				wantProfile = filepath.Join(root, ".onebase", "browser-profiles", "shared")
			}
			if got := os.Getenv("ONEBASE_WEBVIEW_PROFILE"); got != wantProfile {
				t.Fatalf("WebView profile=%q, want %q", got, wantProfile)
			}
		})
	}
}
