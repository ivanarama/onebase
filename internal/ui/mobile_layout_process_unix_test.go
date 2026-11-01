//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package ui

import (
	"errors"
	"os/exec"
	"syscall"
)

func startMobileBrowserProcess(cmd *exec.Cmd) (func() error, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}, nil
}
