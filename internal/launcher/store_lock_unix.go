//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package launcher

import (
	"errors"
	"os"

	"github.com/ivantit66/onebase/internal/fsmode"
	"golang.org/x/sys/unix"
)

type storeFileLock struct {
	file *os.File
}

func acquireStoreFileLock(path string) (*storeFileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fsmode.SecretFile)
	if err != nil {
		return nil, err
	}
	// os.File.Fd stores the platform's int file descriptor in uintptr.
	fd := int(f.Fd()) //nolint:gosec // G115: round-trip of an OS int descriptor, not untrusted numeric input
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return &storeFileLock{file: f}, nil
}

func (l *storeFileLock) Unlock() error {
	if l == nil || l.file == nil {
		return nil
	}
	// os.File.Fd stores the platform's int file descriptor in uintptr.
	fd := int(l.file.Fd()) //nolint:gosec // G115: round-trip of an OS int descriptor, not untrusted numeric input
	err := unix.Flock(fd, unix.LOCK_UN)
	return errors.Join(err, l.file.Close())
}
