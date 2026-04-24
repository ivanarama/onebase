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
	swRestore     = 9
	hwndTopmost   = ^uintptr(0) // -1
	hwndNoTopmost = ^uintptr(1) // -2
	swpNoMove     = 0x0002
	swpNoSize     = 0x0001
)

// OpenWindow opens the launcher in a native webview window and blocks until
// the window is closed or done is closed (via /quit).
// MUST be called from the main goroutine — webview requires the main OS thread.
func OpenWindow(url, title string, done <-chan struct{}) error {
	runtime.LockOSThread()

	// false = production mode; debug mode can fail silently when launched
	// via ShellExecute (double-click from Explorer).
	w := webview.New(false)
	defer w.Destroy()

	w.SetTitle(title)
	w.SetSize(900, 600, webview.HintNone)

	// Ask the page to focus itself once loaded.
	w.Init(`window.addEventListener('load', function(){ window.focus(); });`)
	w.Navigate(url)

	// Win32 foreground fix: when launched via double-click, Explorer restricts
	// foreground activation. Poll for the HWND then force the window to front.
	go bringToFront(w)

	// Close window when /quit is received from the launcher UI.
	go func() {
		<-done
		w.Dispatch(func() { w.Terminate() })
	}()

	w.Run()
	return nil
}

// bringToFront polls until webview exposes its Win32 HWND, then raises the
// window. Needed because double-click via ShellExecute doesn't grant
// foreground rights automatically.
func bringToFront(w webview.WebView) {
	var hwnd uintptr
	for i := 0; i < 20; i++ { // poll up to 10 s
		time.Sleep(500 * time.Millisecond)
		hwnd = uintptr(w.Window())
		if hwnd != 0 {
			break
		}
	}
	if hwnd == 0 {
		return
	}
	w.Dispatch(func() {
		// Make topmost briefly, restore, bring to front, remove topmost.
		procSetWndPos.Call(hwnd, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize)
		procShowWindow.Call(hwnd, swRestore)
		procSetFgWnd.Call(hwnd)
		procSetWndPos.Call(hwnd, hwndNoTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize)
	})
}
