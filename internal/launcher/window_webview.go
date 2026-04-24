//go:build webview

package launcher

import (
	"runtime"

	webview "github.com/webview/webview_go"
)

// OpenWindow opens the launcher in a native webview window and blocks until
// the window is closed or done is closed (via /quit).
// MUST be called from the main goroutine — webview requires the main OS thread.
func OpenWindow(url, title string, done <-chan struct{}) error {
	runtime.LockOSThread()
	w := webview.New(true)
	defer w.Destroy()
	w.SetTitle(title)
	w.SetSize(900, 600, webview.HintNone)
	w.Navigate(url)

	// Allow /quit endpoint to close the window from another goroutine
	go func() {
		<-done
		w.Dispatch(func() { w.Terminate() })
	}()

	w.Run()
	return nil
}
