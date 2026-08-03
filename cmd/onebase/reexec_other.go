//go:build !windows

package main

import (
	"os"
	"os/exec"
)

func reexec() {
	exe, err := os.Executable()
	if err == nil {
		cmd := exec.Command(exe, "start")
		cmd.Stdin = nil
		cmd.Stdout = nil
		cmd.Stderr = nil
		// Перезапуск best-effort: если не стартовал, продолжаем в текущем
		// процессе — это и есть запасной путь.
		_ = cmd.Start()
	}
}
