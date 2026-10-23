package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

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
	if userdata.PortableRoot() != "" && os.Getenv("ONEBASE_WEBVIEW_PROFILE") == "" {
		profile, err := userdata.Path("browser-profiles", "shared")
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
