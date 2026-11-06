package main

import (
	"os/exec"
	"syscall"
)

func preparePipelineGraphQLCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
