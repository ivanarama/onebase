package cli

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/ivantit66/onebase/internal/userdata"
)

// PrepareUserData selects portable storage before startup logs or update state
// can touch the regular profile. Errors use the GUI-aware startup error path.
func PrepareUserData() bool {
	// Startup preparation precedes Cobra; honour the console-only flag even
	// when selecting the portable directory itself fails.
	for _, arg := range os.Args[1:] {
		if arg == "--" {
			break
		}
		if arg == "--no-gui" {
			noGUI = true
		} else if value, ok := strings.CutPrefix(arg, "--no-gui="); ok {
			if parsed, err := strconv.ParseBool(value); err == nil {
				noGUI = parsed
			}
		}
	}
	if err := userdata.Prepare(os.Args[1:]); err != nil {
		showError(fmt.Sprintf("Ошибка запуска OneBase:\n\n%s", err))
		return false
	}
	if userdata.PortableRoot() != "" {
		profile := os.Getenv("ONEBASE_WEBVIEW_PROFILE")
		var err error
		if profile == "" {
			profile, err = userdata.Path("browser-profiles", "shared")
		}
		// The Windows WebView backend reads this variable into a MAX_PATH
		// buffer and silently uses APPDATA when it does not fit. Reject both
		// the shared profile and an explicitly inherited isolated profile
		// before opening any window. Windows counts UTF-16 code units.
		if err == nil && runtime.GOOS == "windows" && len(utf16.Encode([]rune(profile))) >= 260 {
			err = fmt.Errorf("WebView profile path exceeds 259 UTF-16 code units; move the portable bundle to a shorter path")
		}
		if err == nil {
			err = os.Setenv("ONEBASE_WEBVIEW_PROFILE", profile)
		}
		if err != nil {
			showError(fmt.Sprintf("Ошибка настройки переносимого профиля WebView:\n\n%s", err))
			return false
		}
	}
	return true
}
