package main

import (
	"os"
	"os/exec"
	"syscall"

	oblog "github.com/ivantit66/onebase/internal/logging"
)

func reexec() {
	exe, err := os.Executable()
	if err == nil {
		//nolint:gosec // G204: exact current executable from os.Executable, fixed start argument, no shell.
		cmd := exec.Command(exe, "start")
		cmd.Stdin = nil
		cmd.Stdout = nil
		cmd.Stderr = nil
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
		}
		if err := cmd.Start(); err != nil {
			oblog.Component("cli").Warn("cannot start launcher child", "err", err)
		}
	}
}
