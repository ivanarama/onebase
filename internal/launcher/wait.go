package launcher

import (
	"os"
	"os/signal"
	"syscall"
)

// WaitForClose blocks until the process receives a termination signal or the
// launcher server is stopped through /quit. It is the no-GUI counterpart of
// OpenWindow and is also shared by the browser build after opening the URL.
func WaitForClose(done <-chan struct{}) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)
	select {
	case <-quit:
	case <-done:
	}
}
