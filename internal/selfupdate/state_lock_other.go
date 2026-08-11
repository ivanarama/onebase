//go:build js || plan9 || wasip1

package selfupdate

// These targets do not expose a portable advisory file-locking primitive and
// do not run OneBase's multi-process launcher/update workflow. stateMu still
// serializes the package within the single runtime.
type stateFileLock struct{}

func acquireStateFileLock(string) (*stateFileLock, error) { return &stateFileLock{}, nil }

func acquireStateReadLock(string) (*stateFileLock, error) { return nil, nil }

func (*stateFileLock) Unlock() error { return nil }
