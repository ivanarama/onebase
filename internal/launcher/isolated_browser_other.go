//go:build !windows

package launcher

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
)

// isolatedBrowserCommand строит команду запуска изолированного окна.
// Linux — бинарь из PATH; macOS — через `open -na <app> --args`
// (наличие приложения проверяет `open -Ra`, не запуская его).
func isolatedBrowserCommand(profileDir, url string) (*exec.Cmd, error) {
	args := chromiumArgs(profileDir, url)
	if runtime.GOOS == "darwin" {
		for _, app := range []string{"Google Chrome", "Microsoft Edge", "Chromium"} {
			if exec.Command("open", "-Ra", app).Run() == nil { //nolint:gosec // G204: имя программы из фиксированного списка браузеров, аргументы — собранный нами URL
				openArgs := append([]string{"-na", app, "--args"}, args...)
				return exec.Command("open", openArgs...), nil //nolint:gosec // G204: имя программы из фиксированного списка браузеров, аргументы — собранный нами URL
			}
		}
		return nil, i18nerr.Errorf("не найден Chromium-совместимый браузер (Chrome/Edge/Chromium) — откройте обычное окно «Предприятие»")
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge"} {
		if p, err := exec.LookPath(name); err == nil {
			return exec.Command(p, args...), nil //nolint:gosec // G204: имя программы из фиксированного списка браузеров, аргументы — собранный нами URL
		}
	}
	return nil, i18nerr.Errorf("не найден Chromium-совместимый браузер (Chrome/Chromium/Edge) — откройте обычное окно «Предприятие»")
}

// profileInUse: Chromium на POSIX создаёт в профиле симлинк SingletonLock со
// значением «host-pid». Живой pid — профиль занят; битый симлинк или мёртвый
// pid (после падения браузера) — свободен.
func profileInUse(dir string) bool {
	target, err := os.Readlink(filepath.Join(dir, "SingletonLock"))
	if err != nil {
		return false
	}
	i := strings.LastIndex(target, "-")
	if i < 0 {
		return false
	}
	pid, err := strconv.Atoi(target[i+1:])
	if err != nil || pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
