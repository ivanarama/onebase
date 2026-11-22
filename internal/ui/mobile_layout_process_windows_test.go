//go:build windows

package ui

import (
	"fmt"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A dedicated Job Object owns only this test's browser. Closing it terminates
// its descendants, including Edge background/renderer processes, even if the
// test binary exits unexpectedly. It never targets the user's browser profile.
func startMobileBrowserProcess(cmd *exec.Cmd) (func() error, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	//nolint:gosec // G103: synchronous WinAPI receives our correctly typed local struct and its exact sizeof, not input-derived memory.
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	//nolint:gosec // G115: cmd.Start gets a DWORD ProcessId; uint32 reverses Go syscall.StartProcess widening to int.
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, process)
		_ = windows.CloseHandle(process)
	}
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("own browser process tree: %w", err)
	}
	return func() error { return windows.CloseHandle(job) }, nil
}
