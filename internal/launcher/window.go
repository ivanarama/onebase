//go:build !webview

package launcher

import (
	"os"
	"os/signal"
	"syscall"
)

// OpenWindow opens the launcher URL in the default system browser and blocks
// until the process receives a signal or done is closed (via /quit).
func OpenWindow(url, title string, done <-chan struct{}) error {
	OpenBrowser(url)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-quit:
	case <-done:
	}
	return nil
}
