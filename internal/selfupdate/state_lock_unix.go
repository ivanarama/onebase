//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package selfupdate

import (
	"errors"
	"os"

	"github.com/ivantit66/onebase/internal/fsmode"
	"golang.org/x/sys/unix"
)

type stateFileLock struct {
	file *os.File
}

func acquireStateFileLock(path string) (*stateFileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fsmode.File)
	if err != nil {
		return nil, err
	}
	return lockStateFile(f, unix.F_WRLCK)
}

func acquireTargetFileLock(path string) (*stateFileLock, error) {
	f, err := openTargetLockFile(path)
	if err != nil {
		return nil, err
	}
	return lockStateFileRange(f, unix.F_WRLCK, 0)
}

func acquireTargetReadFileLock(path string) (*stateFileLock, error) {
	f, err := openTargetLockFile(path)
	if err != nil {
		return nil, err
	}
	return lockStateFileRange(f, unix.F_RDLCK, 0)
}

func acquireTargetIntentFileLock(path string) (*stateFileLock, error) {
	f, err := openTargetLockFile(path)
	if err != nil {
		return nil, err
	}
	return lockStateFileRange(f, unix.F_WRLCK, 0)
}

func acquireStateReadLock(path string) (*stateFileLock, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is the fixed update-state lock file
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return lockStateFile(f, unix.F_RDLCK)
}

func lockStateFile(f *os.File, lockType int16) (*stateFileLock, error) {
	return lockStateFileRange(f, lockType, 0)
}

func lockStateFileRange(f *os.File, lockType int16, offset int64) (*stateFileLock, error) {
	lock := unix.Flock_t{
		Type:   lockType,
		Whence: 0,
		Start:  offset,
		Len:    1,
	}
	var err error
	for {
		err = unix.FcntlFlock(f.Fd(), unix.F_SETLKW, &lock)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return &stateFileLock{file: f}, nil
}

func (l *stateFileLock) Unlock() error {
	if l == nil || l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	lock := unix.Flock_t{
		Type:   unix.F_UNLCK,
		Whence: 0,
		Start:  0,
		Len:    1,
	}
	err := unix.FcntlFlock(f.Fd(), unix.F_SETLK, &lock)
	return errors.Join(err, f.Close())
}
