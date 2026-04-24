//go:build windows

package cli

import (
	"syscall"
	"unsafe"
)

// showError shows a Windows MessageBox — works even without a console window.
func showError(msg string) {
	dll := syscall.NewLazyDLL("user32.dll")
	proc := dll.NewProc("MessageBoxW")
	title, _ := syscall.UTF16PtrFromString("onebase — Ошибка запуска")
	text, _ := syscall.UTF16PtrFromString(msg)
	proc.Call(0,
		uintptr(unsafe.Pointer(text)),
		uintptr(unsafe.Pointer(title)),
		0x10) // MB_ICONERROR
}
