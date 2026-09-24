//go:build !windows

package main

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

type cacheFileLock struct {
	file       *os.File
	descriptor int
}

func acquireCacheFileLock(ctx context.Context, path string) (*cacheFileLock, error) {
	// path is produced by restResponseCache.lockPath from a SHA-256 digest under
	// the explicit operator-selected cache directory.
	//nolint:gosec // G703: path cannot contain request-controlled path segments.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	// On Unix, os.File.Fd returns the same kernel int descriptor widened to
	// uintptr. The file is open here, so narrowing it restores the original type.
	descriptor := int(file.Fd()) //nolint:gosec // G115: Unix file descriptors originate as int values.
	for {
		err = unix.Flock(descriptor, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return &cacheFileLock{file: file, descriptor: descriptor}, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			_ = file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (lock *cacheFileLock) Close() error {
	unlockErr := unix.Flock(lock.descriptor, unix.LOCK_UN)
	closeErr := lock.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
