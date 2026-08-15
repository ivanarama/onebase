//go:build windows

package interpreter

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"golang.org/x/sys/windows"
)

// runExecCommand assigns the command to a Job Object. Cancellation terminates
// every descendant, including processes that retain stdout/stderr handles after
// the direct child exits. A normally detached child is left alone for backwards
// compatibility when neither cancellation nor WaitDelay fired.
func runExecCommand(cmd *exec.Cmd) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("create command job: %w", err)
	}

	var jobMu sync.Mutex
	jobClosed := false
	closeJob := func() {
		jobMu.Lock()
		defer jobMu.Unlock()
		if !jobClosed {
			jobClosed = true
			_ = windows.CloseHandle(job)
		}
	}
	defer closeJob()

	cmd.Cancel = func() error {
		jobMu.Lock()
		defer jobMu.Unlock()
		if jobClosed {
			return os.ErrProcessDone
		}
		if err := windows.TerminateJobObject(job, 1); err != nil {
			if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_INVALID_HANDLE) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}

	// Keep a cancellation callback from terminating an empty job in the small
	// interval between Start and AssignProcessToJobObject.
	jobMu.Lock()
	if err := cmd.Start(); err != nil {
		jobMu.Unlock()
		return err
	}
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err == nil {
		err = windows.AssignProcessToJobObject(job, process)
		_ = windows.CloseHandle(process)
	}
	if err != nil {
		// Wait receives the watchCtx result, and watchCtx may be trying to run
		// cmd.Cancel after a concurrent context cancellation. Do not keep jobMu
		// across Wait here: Cancel needs the same mutex and assignment has
		// already failed, so there is no protected process tree to serialize.
		jobMu.Unlock()
		_ = cmd.Process.Kill()
		waitErr := cmd.Wait()
		if waitErr != nil {
			return errors.Join(fmt.Errorf("protect command process tree: %w", err), waitErr)
		}
		return fmt.Errorf("protect command process tree: %w", err)
	}
	jobMu.Unlock()

	waitErr := cmd.Wait()
	if errors.Is(waitErr, exec.ErrWaitDelay) {
		jobMu.Lock()
		if !jobClosed {
			_ = windows.TerminateJobObject(job, 1)
		}
		jobMu.Unlock()
	}
	return waitErr
}
