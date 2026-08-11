//go:build webview

package launcher

import (
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	webview "github.com/webview/webview_go"
)

var (
	user32            = syscall.NewLazyDLL("user32.dll")
	procSetFgWnd      = user32.NewProc("SetForegroundWindow")
	procShowWindow    = user32.NewProc("ShowWindow")
	procSetWndPos     = user32.NewProc("SetWindowPos")
	procMessageBox    = user32.NewProc("MessageBoxW")
	procCallWndProc   = user32.NewProc("CallWindowProcW")
	procSetWndLongPtr = user32.NewProc("SetWindowLongPtrW")
	procSetWndLong    = user32.NewProc("SetWindowLongW")
)

const (
	swHide        = 0
	swRestore     = 9
	hwndTopmost   = ^uintptr(0) // -1
	hwndNoTopmost = ^uintptr(1) // -2
	swpNoMove     = 0x0002
	swpNoSize     = 0x0001

	gwlpWndProc = ^uintptr(3) // GWLP_WNDPROC = -4

	wmClose = 0x0010

	mbYesNoCancel   = 0x00000003
	mbIconQuestion  = 0x00000020
	mbSetForeground = 0x00010000
	idCancel        = 2
	idYes           = 6
	idNo            = 7

	// closeStopTimeout — сколько ждать остановки баз, прежде чем выйти всё
	// равно: окно к этому моменту уже скрыто, и висящий невидимый процесс
	// лаунчера хуже, чем недобитая база (её видно в списке при следующем старте).
	closeStopTimeout = 60 * time.Second
)

// Перехват системного крестика окна лаунчера (запрос клиента: «продолжить
// работу в фоновом режиме?»). Вендоренный webview.h на WM_CLOSE сразу зовёт
// DestroyWindow и никакого хука не даёт, поэтому окно подклассируется: наш
// WndProc решает судьбу закрытия, а всё остальное отдаёт прежнему обработчику.
//
// Переменные ниже пишутся один раз при установке хука (в потоке окна, до
// возврата в цикл сообщений) и дальше только читаются.
var (
	closeHookOrig      uintptr
	closeHookWV        webview.WebView
	closeHookCC        CloseCoordinator
	closeHookInstalled bool
	closeHookStopping  atomic.Bool

	closeHookCallback = syscall.NewCallback(launcherWndProc)
)

// OpenWindow opens the launcher in a native webview window and blocks until
// the window is closed or done is closed (via /quit).
// MUST be called from the main goroutine — webview requires the main OS thread.
//
// cc (может быть nil — например, у вторичных окон `onebase window`) решает, что
// делать с работающими базами при закрытии окна: см. closepolicy.go.
func OpenWindow(url, title string, done <-chan struct{}, cc CloseCoordinator) error {
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

	// Win32 foreground fix + перехват закрытия: и то и другое требует HWND,
	// который появляется не сразу.
	go prepareWindow(w, cc)

	// Close window when /quit is received from the launcher UI.
	go func() {
		<-done
		w.Dispatch(func() { w.Terminate() })
	}()

	w.Run()
	return nil
}

// prepareWindow polls until webview exposes its Win32 HWND, then raises the
// window and installs the close hook. Foreground: needed because double-click
// via ShellExecute doesn't grant foreground rights automatically.
func prepareWindow(w webview.WebView, cc CloseCoordinator) {
	var hwnd uintptr
	for i := 0; i < 100; i++ { // poll up to 10 s
		time.Sleep(100 * time.Millisecond)
		hwnd = uintptr(w.Window())
		if hwnd != 0 {
			break
		}
	}
	if hwnd == 0 {
		return
	}
	w.Dispatch(func() {
		installCloseHook(hwnd, w, cc)
		// Make topmost briefly, restore, bring to front, remove topmost.
		procSetWndPos.Call(hwnd, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize)
		procShowWindow.Call(hwnd, swRestore)
		procSetFgWnd.Call(hwnd)
		procSetWndPos.Call(hwnd, hwndNoTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize)
	})
}

// installCloseHook подклассирует окно. Вызывается из потока окна, пока цикл
// сообщений стоит в Dispatch, — поэтому WndProc не может выстрелить раньше,
// чем closeHookOrig получит значение.
func installCloseHook(hwnd uintptr, w webview.WebView, cc CloseCoordinator) {
	if cc == nil || hwnd == 0 || closeHookInstalled {
		return
	}
	closeHookWV, closeHookCC = w, cc
	orig, err := setWindowLongPtr(hwnd, gwlpWndProc, closeHookCallback)
	if orig == 0 {
		// Не смертельно: крестик просто останется «молчаливым», как раньше.
		respondLog().Warn("не удалось перехватить закрытие окна лаунчера", "err", err)
		closeHookCC = nil
		return
	}
	closeHookOrig = orig
	closeHookInstalled = true
}

// setWindowLongPtr: на 32-битной Windows SetWindowLongPtrW — макрос над
// SetWindowLongW, и в user32.dll такого экспорта нет.
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
		return 0, callErr
	}
	return ret, nil
}

func launcherWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	if !closeHookInstalled {
		// Хук ставится вместе с адресом прежнего обработчика; без него звать
		// нечего — но и сюда мы тогда попасть не могли.
		return 0
	}
	if msg == wmClose && handleWindowClose(hwnd) {
		return 0 // закрытие отменено или отложено до остановки баз
	}
	ret, _, _ := procCallWndProc.Call(closeHookOrig, hwnd, msg, wparam, lparam)
	return ret
}

// handleWindowClose решает судьбу крестика. true — сообщение проглочено,
// окно сейчас не закрываем.
func handleWindowClose(hwnd uintptr) bool {
	if closeHookCC == nil {
		return false
	}
	if closeHookStopping.Load() {
		return true // базы уже останавливаются, окно скрыто — ждём
	}
	running := closeHookCC.RunningBases()
	switch planForClose(closeHookCC.OnClosePolicy(), len(running)) {
	case planKeepRunning:
		return false
	case planStopAll:
		return stopBasesThenClose(hwnd)
	}

	lang := currentLang()
	switch messageBox(hwnd, closeDialogText(lang, running), tr(lang, "Закрытие окна информационных баз")) {
	case idYes:
		return false // базы остаются работать — окно закрывается штатно
	case idNo:
		return stopBasesThenClose(hwnd)
	case idCancel:
		return true
	default:
		// Диалог закрыли крестиком или Esc — это тоже «Отмена».
		return true
	}
}

// stopBasesThenClose прячет окно и останавливает базы в фоне: StopAll на каждый
// порт ходит в PowerShell и ждёт освобождения — держать на этом поток окна
// нельзя, замерший на секунды крестик выглядит как зависание.
func stopBasesThenClose(hwnd uintptr) bool {
	closeHookStopping.Store(true)
	procShowWindow.Call(hwnd, swHide)

	wv, cc := closeHookWV, closeHookCC
	go func() {
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			cc.StopAllBases()
		}()
		select {
		case <-stopped:
		case <-time.After(closeStopTimeout):
			respondLog().Warn("базы не остановились за отведённое время — закрываем лаунчер",
				"timeout", closeStopTimeout)
		}
		wv.Dispatch(func() { wv.Terminate() })
	}()
	return true
}

// messageBox показывает системный вопрос с кнопками Да/Нет/Отмена.
// Ошибку показа трактуем как «Да»: не запирать же пользователя в окне из-за
// того, что диалог не создался.
func messageBox(hwnd uintptr, text, caption string) int {
	textPtr, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return idYes
	}
	captionPtr, err := syscall.UTF16PtrFromString(caption)
	if err != nil {
		return idYes
	}
	ret, _, _ := procMessageBox.Call(hwnd,
		uintptr(unsafe.Pointer(textPtr)), uintptr(unsafe.Pointer(captionPtr)),
		mbYesNoCancel|mbIconQuestion|mbSetForeground)
	runtime.KeepAlive(textPtr)
	runtime.KeepAlive(captionPtr)
	if ret == 0 {
		return idYes
	}
	return int(ret)
}
