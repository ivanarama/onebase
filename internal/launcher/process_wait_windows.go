//go:build windows

package launcher

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

type nativeProcessExitWaiter struct {
	handle windows.Handle
}

func newProcessExitWaiter(pid int) (processExitWaiter, error) {
	processID, err := checkedWindowsProcessID(pid)
	if err != nil {
		return nil, err
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, processID)
	if err != nil {
		return nil, err
	}
	return &nativeProcessExitWaiter{handle: handle}, nil
}

func (w *nativeProcessExitWaiter) Wait(timeout time.Duration) bool {
	millis := checkedWindowsWaitMillis(timeout)
	status, err := windows.WaitForSingleObject(w.handle, millis)
	return err == nil && status == windows.WAIT_OBJECT_0
}

func (w *nativeProcessExitWaiter) Close() error {
	return windows.CloseHandle(w.handle)
}

func checkedWindowsProcessID(pid int) (uint32, error) {
	if pid <= 0 || int64(pid) > int64(^uint32(0)) {
		return 0, fmt.Errorf("invalid PID %d", pid)
	}
	return uint32(pid), nil
}

func checkedWindowsWaitMillis(timeout time.Duration) uint32 {
	const maxFiniteWaitMillis = uint32(windows.INFINITE - 1)
	const maxFiniteWaitDuration = time.Duration(maxFiniteWaitMillis) * time.Millisecond
	if timeout <= 0 {
		return 0
	}
	if timeout >= maxFiniteWaitDuration {
		return maxFiniteWaitMillis
	}
	// WaitForSingleObject accepts whole milliseconds. Round a positive
	// sub-millisecond remainder up so a requested wait never becomes a poll.
	millis := (timeout + time.Millisecond - 1) / time.Millisecond
	return uint32(millis)
}
