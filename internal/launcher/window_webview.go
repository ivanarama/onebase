//go:build webview

package launcher

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	webview "github.com/webview/webview_go"
)

var (
	user32              = syscall.NewLazyDLL("user32.dll")
	procSetFgWnd        = user32.NewProc("SetForegroundWindow")
	procShowWindow      = user32.NewProc("ShowWindow")
	procSetWndPos       = user32.NewProc("SetWindowPos")
	procMessageBox      = user32.NewProc("MessageBoxW")
	procCallWndProc     = user32.NewProc("CallWindowProcW")
	procDefWndProc      = user32.NewProc("DefWindowProcW")
	procPostMessage     = user32.NewProc("PostMessageW")
	procPostThreadMsg   = user32.NewProc("PostThreadMessageW")
	procPostQuitMessage = user32.NewProc("PostQuitMessage")
	procSetWndLongPtr   = user32.NewProc("SetWindowLongPtrW")
	procSetWndLong      = user32.NewProc("SetWindowLongW")
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	procCurrentThreadID = kernel32.NewProc("GetCurrentThreadId")
	closeHookCallback   = syscall.NewCallback(launcherWndProc)
	activeWindowSession atomic.Pointer[windowSession]
)

const (
	swHide        = 0
	swRestore     = 9
	hwndTopmost   = ^uintptr(0) // -1
	hwndNoTopmost = ^uintptr(1) // -2
	swpNoMove     = 0x0002
	swpNoSize     = 0x0001

	gwlpWndProc = ^uintptr(3) // GWLP_WNDPROC = -4

	wmClose     = 0x0010
	wmQuit      = 0x0012
	wmNCDestroy = 0x0082
	wmApp       = 0x8000

	// Private messages are sent only to the subclassed top-level launcher HWND.
	wmCloseStopTimeout  = wmApp + 0x151
	wmCloseStopFinished = wmApp + 0x152

	mbYesNoCancel   = 0x00000003
	mbIconError     = 0x00000010
	mbIconQuestion  = 0x00000020
	mbSetForeground = 0x00010000
	idYes           = 6
	idNo            = 7

	closeStopTimeout = 60 * time.Second
)

type windowPhase uint8

const (
	windowIdle windowPhase = iota
	windowCheckingClose
	windowStopping
	windowTerminating
	windowEnded
)

// windowSession is the sole owner of a native window's close lifecycle.
//
// The Win32 callback and OpenWindow cleanup run on the locked UI thread. The
// done watcher and stop workers never retain or call the C++ webview object;
// they communicate through the UI thread's queue and are joined before
// Destroy. This keeps a late /quit or StopAll result from touching freed native
// memory.
type windowSession struct {
	mu sync.Mutex

	hwnd       uintptr
	uiThreadID uint32
	cc         CloseCoordinator

	phase    windowPhase
	runEnded bool

	origWndProc   uintptr
	hookInstalled bool // UI-thread only

	stopErr      error
	stopTimedOut bool
	timeoutAck   chan struct{}

	runEndedCh   chan struct{}
	runEndedOnce sync.Once
	workers      sync.WaitGroup

	doneCancel chan struct{}
	doneExited chan struct{}
}

func newWindowSession(hwnd uintptr, uiThreadID uint32, cc CloseCoordinator) *windowSession {
	return &windowSession{
		hwnd:       hwnd,
		uiThreadID: uiThreadID,
		cc:         cc,
		phase:      windowIdle,
		runEndedCh: make(chan struct{}),
	}
}

// OpenWindow opens the launcher in a native webview window and blocks until
// the window is closed or done is closed (via /quit).
// MUST be called from the main goroutine; webview requires the main OS thread.
func OpenWindow(url, title string, done <-chan struct{}, cc CloseCoordinator) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// false = production mode; debug mode can fail silently when launched via
	// ShellExecute (double-click from Explorer).
	w := webview.New(false)
	w.SetTitle(title)
	w.SetSize(900, 600, webview.HintNone)
	w.Init(`window.addEventListener('load', function(){ window.focus(); });`)
	w.Navigate(url)

	// The vendored Win32 backend creates and shows its HWND synchronously in
	// webview.New. Install the hook now, on the owning UI thread, before Run can
	// process even the first WM_CLOSE.
	hwnd := uintptr(w.Window())
	threadID, _, _ := procCurrentThreadID.Call()
	session := newWindowSession(hwnd, uint32(threadID), cc)
	if hwnd != 0 {
		if err := session.installCloseHook(); err != nil {
			respondLog().Warn("не удалось перехватить закрытие окна лаунчера", "err", err)
		}
		bringWindowToFront(hwnd)
	}

	session.startDoneWatcher(done)
	w.Run()

	// No goroutine may outlive the native object. In the normal stop path Run
	// can return only after StopAll completed successfully. If the loop ended by
	// an external native event, waiting here deliberately keeps Destroy away
	// from an uncancellable StopAll call.
	session.markRunEnded()
	session.stopDoneWatcher()
	session.workers.Wait()
	session.uninstallCloseHook()
	session.reset()
	w.Destroy()
	return nil
}

func bringWindowToFront(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	procSetWndPos.Call(hwnd, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize)
	procShowWindow.Call(hwnd, swRestore)
	procSetFgWnd.Call(hwnd)
	procSetWndPos.Call(hwnd, hwndNoTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize)
}

func (s *windowSession) installCloseHook() error {
	if s.hwnd == 0 {
		return fmt.Errorf("native HWND is unavailable")
	}
	if activeWindowSession.Load() != nil {
		return fmt.Errorf("another launcher close hook is still active")
	}

	orig, err := setWindowLongPtr(s.hwnd, gwlpWndProc, closeHookCallback)
	if orig == 0 {
		return err
	}
	s.origWndProc = orig
	s.hookInstalled = true
	if !activeWindowSession.CompareAndSwap(nil, s) {
		_, restoreErr := setWindowLongPtr(s.hwnd, gwlpWndProc, orig)
		s.hookInstalled = false
		if restoreErr != nil {
			return fmt.Errorf("close hook raced with another window; restore: %w", restoreErr)
		}
		return fmt.Errorf("close hook raced with another window")
	}
	return nil
}

// uninstallCloseHook runs only on the owning UI thread. It is safe both from
// WM_NCDESTROY and from the orderly WM_QUIT -> Run return path.
func (s *windowSession) uninstallCloseHook() {
	if !s.hookInstalled {
		activeWindowSession.CompareAndSwap(s, nil)
		return
	}
	hwnd, orig := s.hwnd, s.origWndProc
	if hwnd != 0 && orig != 0 {
		if _, err := setWindowLongPtr(hwnd, gwlpWndProc, orig); err != nil {
			respondLog().Warn("не удалось восстановить обработчик окна лаунчера", "err", err)
		}
	}
	s.hookInstalled = false
	activeWindowSession.CompareAndSwap(s, nil)
}

// setWindowLongPtr uses SetWindowLongW on 32-bit Windows, where
// SetWindowLongPtrW is a preprocessor alias and is not exported by user32.dll.
func setWindowLongPtr(hwnd, index, value uintptr) (uintptr, error) {
	proc := procSetWndLongPtr
	if err := proc.Find(); err != nil {
		proc = procSetWndLong
		if err := proc.Find(); err != nil {
			return 0, err
		}
	}
	ret, _, callErr := proc.Call(hwnd, index, value)
	if ret == 0 {
		return 0, win32CallError("SetWindowLongPtrW", callErr)
	}
	return ret, nil
}

func launcherWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	s := activeWindowSession.Load()
	if s == nil || s.hwnd != hwnd {
		ret, _, _ := procDefWndProc.Call(hwnd, msg, wparam, lparam)
		return ret
	}

	orig := s.origWndProc
	switch msg {
	case wmClose:
		if s.handleWindowClose() {
			return 0
		}
	case wmCloseStopTimeout:
		s.handleStopTimeout()
		return 0
	case wmCloseStopFinished:
		s.handleStopFinished()
		return 0
	case wmNCDestroy:
		// Remove instance subclassing in reverse order while the HWND is still
		// valid. Call the saved procedure explicitly for this final message.
		s.uninstallCloseHook()
		s.markRunEnded()
		ret, _, _ := procCallWndProc.Call(orig, hwnd, msg, wparam, lparam)
		return ret
	}

	ret, _, _ := procCallWndProc.Call(orig, hwnd, msg, wparam, lparam)
	return ret
}

// handleWindowClose returns true when WM_CLOSE must be swallowed.
func (s *windowSession) handleWindowClose() bool {
	if !s.beginCloseCheck() {
		return true
	}
	if s.cc == nil {
		s.allowClose()
		return false
	}

	running, policy, err := s.cc.CloseState()
	if err != nil {
		lang := currentLang()
		showNativeError(s.hwnd, closeStateErrorText(lang, err), tr(lang, "Ошибка"))
		s.cancelCloseCheck()
		return true
	}

	switch planForClose(policy, len(running)) {
	case planKeepRunning:
		s.allowClose()
		return false
	case planStopAll:
		s.startStop()
		return true
	}

	lang := currentLang()
	answer, err := nativeMessageBox(s.hwnd, closeDialogText(lang, running),
		tr(lang, "Закрытие окна информационных баз"),
		mbYesNoCancel|mbIconQuestion|mbSetForeground)
	if err != nil {
		respondLog().Warn("не удалось показать вопрос при закрытии лаунчера", "err", err)
		s.cancelCloseCheck()
		return true
	}
	switch answer {
	case idYes:
		s.allowClose()
		return false
	case idNo:
		s.startStop()
		return true
	default: // Cancel, Esc, the dialog's X, or an unknown result.
		s.cancelCloseCheck()
		return true
	}
}

func (s *windowSession) beginCloseCheck() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runEnded || s.phase != windowIdle {
		return false
	}
	s.phase = windowCheckingClose
	return true
}

func (s *windowSession) allowClose() {
	s.mu.Lock()
	if s.phase == windowCheckingClose {
		s.phase = windowTerminating
	}
	s.mu.Unlock()
}

func (s *windowSession) cancelCloseCheck() {
	s.mu.Lock()
	if s.phase == windowCheckingClose {
		s.phase = windowIdle
	}
	s.mu.Unlock()
}

// startStop hides the window and starts an uncancellable coordinator call.
// The monitor remains alive after a timeout until StopAllBases actually
// returns; OpenWindow joins both goroutines before destroying the webview.
func (s *windowSession) startStop() {
	s.mu.Lock()
	if s.runEnded || s.phase != windowCheckingClose {
		s.mu.Unlock()
		return
	}
	s.phase = windowStopping
	s.stopErr = nil
	s.stopTimedOut = false
	s.timeoutAck = make(chan struct{}, 1)
	cc := s.cc
	s.workers.Add(2)
	s.mu.Unlock()

	procShowWindow.Call(s.hwnd, swHide)
	result := make(chan error, 1)
	go func() {
		defer s.workers.Done()
		result <- cc.StopAllBases()
	}()
	go func() {
		defer s.workers.Done()
		s.monitorStop(result)
	}()
}

func (s *windowSession) monitorStop(result <-chan error) {
	timer := time.NewTimer(closeStopTimeout)
	defer timer.Stop()

	select {
	case err := <-result:
		s.recordStopResult(err)
		if postErr := s.postWindowMessage(wmCloseStopFinished); postErr != nil {
			respondLog().Warn("не удалось обработать результат остановки баз", "err", postErr)
		}
		return
	case <-timer.C:
		s.mu.Lock()
		if s.runEnded || s.phase != windowStopping {
			s.mu.Unlock()
			<-result
			return
		}
		s.stopTimedOut = true
		ack := s.timeoutAck
		s.mu.Unlock()
		if err := s.postWindowMessage(wmCloseStopTimeout); err != nil {
			respondLog().Warn("не удалось показать таймаут остановки баз", "err", err)
			select {
			case ack <- struct{}{}:
			default:
			}
		}

		// Do not allow a completion event to re-enter the timeout MessageBox.
		select {
		case <-ack:
		case <-s.runEndedCh:
		}
		err := <-result
		s.recordStopResult(err)
		if postErr := s.postWindowMessage(wmCloseStopFinished); postErr != nil {
			respondLog().Warn("не удалось обработать результат остановки баз после таймаута", "err", postErr)
		}
	}
}

func (s *windowSession) recordStopResult(err error) {
	s.mu.Lock()
	s.stopErr = err
	s.mu.Unlock()
}

func (s *windowSession) postWindowMessage(msg uintptr) error {
	s.mu.Lock()
	if s.runEnded {
		s.mu.Unlock()
		return nil
	}
	hwnd := s.hwnd
	s.mu.Unlock()
	ret, _, callErr := procPostMessage.Call(hwnd, msg, 0, 0)
	if ret == 0 {
		return win32CallError("PostMessageW", callErr)
	}
	return nil
}

func (s *windowSession) handleStopTimeout() {
	s.mu.Lock()
	if s.runEnded || s.phase != windowStopping || !s.stopTimedOut {
		s.mu.Unlock()
		return
	}
	ack := s.timeoutAck
	s.mu.Unlock()

	bringWindowToFront(s.hwnd)
	lang := currentLang()
	showNativeError(s.hwnd, stopTimeoutErrorText(lang), tr(lang, "Ошибка"))
	select {
	case ack <- struct{}{}:
	default:
	}
}

func (s *windowSession) handleStopFinished() {
	s.mu.Lock()
	if s.runEnded || s.phase != windowStopping {
		s.mu.Unlock()
		return
	}
	err, timedOut := s.stopErr, s.stopTimedOut
	if err == nil && !timedOut {
		s.phase = windowTerminating
		s.mu.Unlock()
		// We are in the owning UI thread. Posting WM_QUIT here terminates the
		// exact queue consumed by webview.Run.
		procPostQuitMessage.Call(0)
		return
	}
	s.mu.Unlock()

	bringWindowToFront(s.hwnd)
	if !timedOut {
		lang := currentLang()
		showNativeError(s.hwnd, stopBasesErrorText(lang, err), tr(lang, "Ошибка"))
	} else if err != nil {
		respondLog().Warn("остановка баз завершилась ошибкой после таймаута", "err", err)
	}

	// Keep phase=stopping while the error dialog is open, so a concurrent
	// /quit cannot close the process underneath the failed stop. Once the user
	// has acknowledged the error (and the worker has returned), closing may be
	// attempted again.
	s.mu.Lock()
	if !s.runEnded && s.phase == windowStopping {
		s.phase = windowIdle
		s.stopErr = nil
		s.stopTimedOut = false
		s.timeoutAck = nil
	}
	s.mu.Unlock()
}

func (s *windowSession) startDoneWatcher(done <-chan struct{}) {
	if done == nil {
		return
	}
	s.doneCancel = make(chan struct{})
	s.doneExited = make(chan struct{})
	go func() {
		defer close(s.doneExited)
		select {
		case <-done:
			s.requestDoneTermination()
		case <-s.doneCancel:
		}
	}()
}

func (s *windowSession) requestDoneTermination() {
	s.mu.Lock()
	if s.runEnded || s.phase == windowEnded || s.phase == windowTerminating {
		s.mu.Unlock()
		return
	}
	if s.phase == windowCheckingClose || s.phase == windowStopping {
		// The close check/stop owns the decision. Consuming this one-shot done
		// request is intentional: an error or timeout must leave the window open.
		s.mu.Unlock()
		return
	}
	s.phase = windowTerminating
	threadID := s.uiThreadID
	s.mu.Unlock()

	ret, _, callErr := procPostThreadMsg.Call(uintptr(threadID), wmQuit, 0, 0)
	if ret != 0 {
		return
	}
	respondLog().Warn("не удалось завершить цикл окна лаунчера",
		"err", win32CallError("PostThreadMessageW", callErr))
	s.mu.Lock()
	if !s.runEnded && s.phase == windowTerminating {
		s.phase = windowIdle
	}
	s.mu.Unlock()
}

func (s *windowSession) stopDoneWatcher() {
	if s.doneCancel == nil {
		return
	}
	close(s.doneCancel)
	<-s.doneExited
}

func (s *windowSession) markRunEnded() {
	s.mu.Lock()
	s.runEnded = true
	s.phase = windowEnded
	s.mu.Unlock()
	s.runEndedOnce.Do(func() { close(s.runEndedCh) })
}

func (s *windowSession) reset() {
	activeWindowSession.CompareAndSwap(s, nil)
	s.mu.Lock()
	s.cc = nil
	s.stopErr = nil
	s.timeoutAck = nil
	s.doneCancel = nil
	s.doneExited = nil
	s.runEndedCh = nil
	s.hwnd = 0
	s.uiThreadID = 0
	s.origWndProc = 0
	s.hookInstalled = false
	s.mu.Unlock()
}

func closeStateErrorText(lang string, err error) string {
	if isEnglishLang(lang) {
		return "Could not check running bases:\n\n" + err.Error() +
			"\n\nThe launcher window will remain open."
	}
	return "Не удалось проверить работающие базы:\n\n" + err.Error() +
		"\n\nОкно лаунчера останется открытым."
}

func stopBasesErrorText(lang string, err error) string {
	if isEnglishLang(lang) {
		return "Could not stop all bases:\n\n" + err.Error() +
			"\n\nThe launcher window will remain open."
	}
	return "Не удалось остановить все базы:\n\n" + err.Error() +
		"\n\nОкно лаунчера останется открытым."
}

func stopTimeoutErrorText(lang string) string {
	seconds := int(closeStopTimeout / time.Second)
	if isEnglishLang(lang) {
		return fmt.Sprintf("The bases did not stop within %d seconds.\n\nThe launcher window will remain open until the stop operation finishes.", seconds)
	}
	return fmt.Sprintf("Базы не остановились за %d секунд.\n\nОкно лаунчера останется открытым до завершения остановки.", seconds)
}

func isEnglishLang(lang string) bool {
	return strings.HasPrefix(strings.ToLower(lang), "en")
}

func showNativeError(hwnd uintptr, text, caption string) {
	if _, err := nativeMessageBox(hwnd, text, caption, mbIconError|mbSetForeground); err != nil {
		respondLog().Warn("не удалось показать ошибку закрытия лаунчера", "err", err)
	}
}

func nativeMessageBox(hwnd uintptr, text, caption string, flags uintptr) (int, error) {
	textPtr, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return 0, err
	}
	captionPtr, err := syscall.UTF16PtrFromString(caption)
	if err != nil {
		return 0, err
	}
	ret, _, callErr := procMessageBox.Call(hwnd,
		uintptr(unsafe.Pointer(textPtr)), uintptr(unsafe.Pointer(captionPtr)), flags)
	runtime.KeepAlive(textPtr)
	runtime.KeepAlive(captionPtr)
	if ret == 0 {
		return 0, win32CallError("MessageBoxW", callErr)
	}
	return int(ret), nil
}

func win32CallError(name string, err error) error {
	if err == nil {
		return fmt.Errorf("%s failed", name)
	}
	return fmt.Errorf("%s failed: %w", name, err)
}
