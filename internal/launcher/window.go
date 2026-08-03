//go:build !webview

package launcher

import (
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
)

// OpenWindow opens the launcher URL in the default system browser and blocks
// until the process receives a signal or done is closed (via /quit).
func OpenWindow(url, title string, done <-chan struct{}) error {
	openBrowser(url)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-quit:
	case <-done:
	}
	return nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url) //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
	case "darwin":
		cmd = exec.Command("open", url) //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
	default:
		cmd = exec.Command("xdg-open", url) //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
	}
	noWindow(cmd)
	// Браузер мог не найтись — открыть окно всё равно нечем, но пусть это
	// будет видно: пользователь жалуется «нажал, ничего не произошло».
	bestEffort("открыть URL во внешнем браузере", cmd.Start())
}
