//go:build webview

package launcher

import (
	"runtime"

	webview "github.com/webview/webview_go"
)

func OpenWindow(url, title string) error {
	runtime.LockOSThread()
	w := webview.New(true)
	defer w.Destroy()
	w.SetTitle(title)
	w.SetSize(720, 560, webview.HintNone)
	w.Navigate(url)
	w.Run()
	return nil
}
