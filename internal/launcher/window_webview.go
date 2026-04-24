//go:build webview

package launcher

import (
	"runtime"
	"syscall"
	"time"

	webview "github.com/webview/webview_go"
)

var (
	user32         = syscall.NewLazyDLL("user32.dll")
	procSetFgWnd   = user32.NewProc("SetForegroundWindow")
	procShowWindow = user32.NewProc("ShowWindow")
	procSetWndPos  = user32.NewProc("SetWindowPos")
)

const (
	swRestore    = 9
	hwndTopmost  = ^uintptr(0)  // -1
	hwndNoTopmost = ^uintptr(1) // -2
	swpNoMove    = 0x0002
	swpNoSize    = 0x0001
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

	// Bring window to foreground — required when launched via double-click
	// (ShellExecute restricts foreground activation unlike Shell.Run).
	go func() {
		time.Sleep(300 * time.Millisecond)
		w.Dispatch(func() {
			hwnd := uintptr(w.Window())
			if hwnd == 0 {
				return
			}
			// Make topmost briefly → restore → set foreground
			procSetWndPos.Call(hwnd, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize)
			procShowWindow.Call(hwnd, swRestore)
			procSetFgWnd.Call(hwnd)
			procSetWndPos.Call(hwnd, hwndNoTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize)
		})
	}()

	// Close window when /quit is received
	go func() {
		<-done
		w.Dispatch(func() { w.Terminate() })
	}()

	w.Run()
	return nil
}
