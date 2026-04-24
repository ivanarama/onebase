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
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Start()
}
