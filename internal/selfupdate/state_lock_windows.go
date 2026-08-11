//go:build windows

package selfupdate

import (
	"errors"
	"os"

	"github.com/ivantit66/onebase/internal/fsmode"
	"golang.org/x/sys/windows"
)

type stateFileLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func acquireStateFileLock(path string) (*stateFileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fsmode.File)
	if err != nil {
		return nil, err
	}
	return lockStateFile(f, windows.LOCKFILE_EXCLUSIVE_LOCK)
}

func acquireStateReadLock(path string) (*stateFileLock, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is the fixed update-state lock file
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return lockStateFile(f, 0)
}

func lockStateFile(f *os.File, flags uint32) (*stateFileLock, error) {
	lock := &stateFileLock{file: f}
	if err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		flags,
		0,
		1,
		0,
		&lock.overlapped,
	); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return lock, nil
}

func (l *stateFileLock) Unlock() error {
	if l == nil || l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	err := windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &l.overlapped)
	return errors.Join(err, f.Close())
}
