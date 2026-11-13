//go:build windows

package ui

import (
	"os/exec"
	"syscall"
)

func hideAudioTestProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
