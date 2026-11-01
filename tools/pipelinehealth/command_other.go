//go:build !windows

package main

import "os/exec"

func preparePipelineGraphQLCommand(_ *exec.Cmd) {}
