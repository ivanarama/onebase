//go:build !windows

package launcher

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

type nativeProcessExitWaiter struct {
	process *os.Process
}

func newProcessExitWaiter(pid int) (processExitWaiter, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("invalid PID %d", pid)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return nil, err
	}
	return &nativeProcessExitWaiter{process: process}, nil
}

func (w *nativeProcessExitWaiter) Wait(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		err := w.process.Signal(syscall.Signal(0))
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return true
		}
		if timeout <= 0 || time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (w *nativeProcessExitWaiter) Close() error {
	return w.process.Release()
}
