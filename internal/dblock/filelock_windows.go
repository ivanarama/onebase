//go:build windows

package dblock

import (
	"context"
	"errors"
	"os"
	"sync"

	"github.com/ivantit66/onebase/internal/fsmode"
	"golang.org/x/sys/windows"
)

type fileLease struct {
	mu         sync.Mutex
	file       *os.File
	overlapped windows.Overlapped
	shared     bool
	locked     bool
}

func tryAcquireFileLease(path string, shared bool) (*fileLease, bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fsmode.SecretFile) //nolint:gosec // G703: sole caller appends a fixed lock suffix to the absolute, clean, symlink-resolved SQLite target after validatedSQLiteTargetParent rejects roots and malformed paths
	if err != nil {
		return nil, false, err
	}
	lease := &fileLease{file: f, shared: shared}
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if !shared {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	err = windows.LockFileEx(
		windows.Handle(f.Fd()),
		flags,
		0,
		1,
		0,
		&lease.overlapped,
	)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return nil, false, f.Close()
	}
	if err != nil {
		return nil, false, errors.Join(err, f.Close())
	}
	lease.locked = true
	return lease, true, nil
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
	handle := windows.Handle(l.file.Fd())
	if l.locked {
		if err := windows.UnlockFileEx(handle, 0, 1, 0, &l.overlapped); err != nil {
			return err
		}
		l.locked = false
	}
	l.overlapped = windows.Overlapped{}
	if err := windows.LockFileEx(handle, 0, 0, 1, 0, &l.overlapped); err != nil {
		return err
	}
	l.locked = true
	l.shared = true
	return nil
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
	var err error
	if l.locked {
		err = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &l.overlapped)
		l.locked = false
	}
	return errors.Join(err, f.Close())
}
