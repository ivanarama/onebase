//go:build windows && webview

package launcher

import (
	"os"
	"os/exec"
)

// nativeIsolatedSupported: нативные WebView2-окна доступны в GUI-сборке под
// Windows (план 78, п. 4.2) — на них работает сам лаунчер, runtime заведомо есть.
func nativeIsolatedSupported() bool { return true }

// nativeIsolatedCommand строит запуск нативного окна: сам же exe
// (`onebase-gui window --url ...`). Непустой profileDir задаётся через
// ONEBASE_WEBVIEW_PROFILE (её читает наш патч webview.h в third_party/webview_go)
// — у изолированного окна свой каталог профиля WebView2, а значит свой
// cookie-jar. Пустой profileDir = ОБЩИЙ профиль (%APPDATA%\<exe>, как у самого
// лаунчера): переменную не задаём, окно делит сеанс с Предприятием — это путь
// кнопки «Предприятие».
func nativeIsolatedCommand(profileDir, url string) (*exec.Cmd, bool) {
	exe, err := os.Executable()
	if err != nil {
		return nil, false
	}
	cmd := exec.Command(exe, "window", "--url", url, "--title", "onebase — Предприятие")
	cmd.Env = environmentWithout(os.Environ(), "ONEBASE_WEBVIEW_PROFILE")
	if profileDir != "" {
		cmd.Env = append(cmd.Env, "ONEBASE_WEBVIEW_PROFILE="+profileDir)
	}
	return cmd, true
}
