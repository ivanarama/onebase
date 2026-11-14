//go:build !windows

package ui

import "os/exec"

func hideAudioTestProcess(cmd *exec.Cmd) {}
