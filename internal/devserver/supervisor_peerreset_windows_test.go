package devserver

import (
	"errors"
	"syscall"
)

// Terminating the helper can reset its socket instead of sending FIN on Windows.
func helperPeerReset(err error) bool { return errors.Is(err, syscall.WSAECONNRESET) }
