//go:build windows

package launcher

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
)

// chromiumCandidates возвращает возможные пути Chromium-браузеров в порядке
// приоритета: Edge (предустановлен на Windows) → Chrome → Chromium.
func chromiumCandidates() []string {
	dirs := []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")}
	rels := []string{
		filepath.Join("Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join("Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join("Chromium", "Application", "chrome.exe"),
	}
	var out []string
	for _, rel := range rels {
		for _, d := range dirs {
			if d == "" {
				continue
			}
			out = append(out, filepath.Join(d, rel))
		}
	}
	return out
}

// isolatedBrowserCommand строит команду запуска изолированного окна.
func isolatedBrowserCommand(profileDir, url string) (*exec.Cmd, error) {
	for _, p := range chromiumCandidates() {
		if _, err := os.Stat(p); err == nil {
			//nolint:gosec // G204: fixed browser names under local OS install directories; URL/profile are separate arguments, no shell.
			return exec.Command(p, chromiumArgs(profileDir, url)...), nil
		}
	}
	return nil, i18nerr.Errorf("не найден Chromium-совместимый браузер (Edge/Chrome/Chromium) — откройте обычное окно «Предприятие»")
}

// profileInUse: Chromium держит файл lockfile в корне профиля открытым всё
// время жизни браузера. Windows не даёт удалить открытый файл, поэтому
// пробуем удалить: ошибка — профиль занят; успех или отсутствие файла —
// свободен (stale-lock после падения браузера безвреден, он пересоздастся).
// У нативных WebView2-окон Chromium-профиль лежит в подкаталоге EBWebView —
// проверяем оба расположения.
func profileInUse(dir string) bool {
	for _, lock := range []string{
		filepath.Join(dir, "lockfile"),
		filepath.Join(dir, "EBWebView", "lockfile"),
	} {
		if err := os.Remove(lock); err != nil && !os.IsNotExist(err) {
			return true
		}
	}
	return false
}
