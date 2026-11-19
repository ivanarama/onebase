//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package sheet

import (
	"errors"
	"fmt"
	"math"
	"os"

	"golang.org/x/sys/unix"
)

func tryWorkbookLock(f *os.File) (bool, error) {
	fd := f.Fd()
	if fd > uintptr(math.MaxInt) {
		return false, fmt.Errorf("invalid workbook lock descriptor")
	}
	err := unix.Flock(int(fd), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}
