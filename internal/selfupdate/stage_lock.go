package selfupdate

import (
	"path/filepath"
	"sync"
)

const stageLockFileName = "stage.lock"

// stageMu complements the cross-process stage.lock for POSIX, where record
// locks are process-scoped. It also keeps concurrent goroutines from sharing a
// staging lifecycle while one of them downloads or applies files.
var stageMu sync.Mutex

type stageOperationLock struct {
	fileLock *stateFileLock
	released bool
}

func acquireStageOperationLock() (*stageOperationLock, error) {
	stageMu.Lock()
	updates, err := UpdatesDir()
	if err != nil {
		stageMu.Unlock()
		return nil, err
	}
	fileLock, err := acquireStateFileLock(filepath.Join(updates, stageLockFileName))
	if err != nil {
		stageMu.Unlock()
		return nil, err
	}
	return &stageOperationLock{fileLock: fileLock}, nil
}

func (l *stageOperationLock) Unlock() error {
	if l == nil || l.released {
		return nil
	}
	l.released = true
	err := l.fileLock.Unlock()
	stageMu.Unlock()
	return err
}
