//go:build unix

package reviewrouting

import (
	"os"
	"syscall"
)

func openRegular(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}
