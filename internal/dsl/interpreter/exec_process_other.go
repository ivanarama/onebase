//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package interpreter

import "os/exec"

// WaitDelay in the caller still bounds inherited-pipe waits on less common
// targets whose process APIs do not expose the Unix process-group mechanism.
func runExecCommand(cmd *exec.Cmd) error { return cmd.Run() }
