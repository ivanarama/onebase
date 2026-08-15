//go:build !webview

package launcher

// OpenWindow opens the launcher URL in the default system browser and blocks
// until the process receives a signal or done is closed (via /quit).
//
// cc здесь не используется: у вкладки браузера крестик перехватить нечем —
// диалог закрытия в этой сборке живёт в самой странице (кнопка ✕ на панели,
// см. quitLauncher в templates.go).
func OpenWindow(url, title string, done <-chan struct{}, cc CloseCoordinator) error {
	OpenBrowser(url)
	WaitForClose(done)
	return nil
}
