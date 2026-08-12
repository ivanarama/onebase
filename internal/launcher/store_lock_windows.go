//go:build windows

package launcher

import (
	"errors"
	"os"

	"github.com/ivantit66/onebase/internal/fsmode"
	"golang.org/x/sys/windows"
)

type storeFileLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func acquireStoreFileLock(path string) (*storeFileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fsmode.SecretFile)
	if err != nil {
		return nil, err
	}
	lock := &storeFileLock{file: f}
	if err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		1,
		0,
		&lock.overlapped,
	); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return lock, nil
}

func tryAcquireStoreFileLock(path string) (*storeFileLock, bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fsmode.SecretFile)
	if err != nil {
		return nil, false, err
	}
	lock := &storeFileLock{file: f}
	err = windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&lock.overlapped,
	)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return nil, false, f.Close()
	}
	if err != nil {
		return nil, false, errors.Join(err, f.Close())
	}
	return lock, true, nil
}

func (l *storeFileLock) Unlock() error {
	if l == nil || l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	err := windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &l.overlapped)
	return errors.Join(err, f.Close())
}
