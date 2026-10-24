//go:build !(windows && webview)

package launcher

import "os/exec"

// Нативные WebView2-окна доступны только в GUI-сборке под Windows (план 78,
// п. 4.2) — остальные сборки открывают изолированные окна внешним Chromium.
func nativeIsolatedSupported() bool { return false }

func nativeIsolatedCommand(_, _, _ string) (*exec.Cmd, bool) { return nil, false }
