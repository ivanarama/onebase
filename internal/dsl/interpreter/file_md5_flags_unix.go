//go:build unix

package interpreter

import (
	"os"

	"golang.org/x/sys/unix"
)

// An untrusted filename can resolve to a FIFO. Open without waiting for a
// writer so the subsequent check can reject files other than regular files.
const fileMD5OpenFlags = os.O_RDONLY | unix.O_NONBLOCK
