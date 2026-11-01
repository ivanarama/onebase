//go:build windows

package cli

import (
	"fmt"
	"os"

	oblog "github.com/ivantit66/onebase/internal/logging"
	"golang.org/x/sys/windows"
)

// showError сообщает об ошибке запуска. Текст всегда пишется в stderr. Модальное
// окно MessageBox показывается ТОЛЬКО когда писать ошибку реально некуда —
// то есть stderr недоступен (запуск двойным кликом по onebase.exe без консоли).
// При запуске из консоли, скрипта или CI (stderr ведёт в консоль/пайп/файл)
// окно не показывается — иначе фоновый процесс зависал бы на модалке, которую
// некому закрыть. Флаг --no-gui / ONEBASE_NO_GUI отключает окно принудительно.
func showError(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	if guiErrorsDisabled() || stderrIsUsable() {
		return
	}
	title, err := windows.UTF16PtrFromString("onebase — Ошибка запуска")
	if err != nil {
		oblog.Component("cli").Warn("cannot encode startup error title", "err", err)
		return
	}
	text, err := windows.UTF16PtrFromString(msg)
	if err != nil {
		oblog.Component("cli").Warn("cannot encode startup error message", "err", err)
		return
	}
	if _, err := windows.MessageBox(0, text, title, windows.MB_ICONERROR); err != nil {
		oblog.Component("cli").Warn("cannot display startup error", "err", err)
	}
}

// stderrIsUsable возвращает true, если stderr ведёт в консоль, пайп или файл —
// то есть текст ошибки действительно куда-то попадёт. Для недоступного хэндла
// (нет консоли) GetFileType вернёт FILE_TYPE_UNKNOWN.
func stderrIsUsable() bool {
	h := windows.Handle(os.Stderr.Fd())
	if h == 0 || h == windows.InvalidHandle {
		return false
	}
	kind, err := windows.GetFileType(h)
	return err == nil && kind != windows.FILE_TYPE_UNKNOWN
}
