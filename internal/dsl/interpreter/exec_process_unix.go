//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package interpreter

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// runExecCommand puts the command in a fresh process group. Cancellation kills
// the whole group, not only the direct child; the final kill also catches a
// descendant that outlived a normally exiting parent (including one detected
// by Cmd.WaitDelay because it retained an output pipe).
func runExecCommand(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killExecProcessGroup(cmd) }

	runErr := cmd.Run()
	if !errors.Is(runErr, exec.ErrWaitDelay) {
		return runErr
	}
	// The direct child is already reaped, but a descendant retaining an output
	// pipe keeps the process group alive, so its id cannot have been reused here.
	return errors.Join(runErr, killExecProcessGroup(cmd))
}

func killExecProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if errors.Is(err, os.ErrProcessDone) {
		return os.ErrProcessDone
	}
	return err
}
