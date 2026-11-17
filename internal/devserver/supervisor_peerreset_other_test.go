//go:build !windows

package devserver

import (
	"errors"
	"syscall"
)

func helperPeerReset(err error) bool { return errors.Is(err, syscall.ECONNRESET) }
