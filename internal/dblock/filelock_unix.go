//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package dblock

import (
	"context"
	"errors"
	"os"
	"sync"

	"github.com/ivantit66/onebase/internal/fsmode"
	"golang.org/x/sys/unix"
)

type fileLease struct {
	mu     sync.Mutex
	file   *os.File
	shared bool
}

func tryAcquireFileLease(path string, shared bool) (*fileLease, bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fsmode.SecretFile) //nolint:gosec // G703: sole caller appends a fixed lock suffix to the absolute, clean, symlink-resolved SQLite target after validatedSQLiteTargetParent rejects roots and malformed paths
	if err != nil {
		return nil, false, err
	}
	fd := int(f.Fd()) //nolint:gosec // round-trip of an OS descriptor
	mode := unix.LOCK_EX
	if shared {
		mode = unix.LOCK_SH
	}
	if err := unix.Flock(fd, mode|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, false, f.Close()
		}
		return nil, false, errors.Join(err, f.Close())
	}
	return &fileLease{file: f, shared: shared}, true, nil
}

func (l *fileLease) Downgrade(_ context.Context) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil || l.shared {
		return nil
	}
	for {
		err := unix.Flock(int(l.file.Fd()), unix.LOCK_SH)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err == nil {
			l.shared = true
		}
		return err
	}
}

func (l *fileLease) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	fd := int(f.Fd()) //nolint:gosec // round-trip of an OS descriptor
	err := unix.Flock(fd, unix.LOCK_UN)
	return errors.Join(err, f.Close())
}
