package launcher

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

const (
	// RestartWaitEnv is set only on a launcher process spawned by RestartSelf.
	// That child waits briefly for its parent to release the singleton lease;
	// an ordinary second launcher fails immediately instead of hanging.
	RestartWaitEnv       = "ONEBASE_LAUNCHER_RESTART_WAIT"
	instanceLockFileName = "launcher.instance.lock"
	instanceWaitTimeout  = 15 * time.Second
)

// LauncherInstance owns the per-user launcher singleton lease.
type LauncherInstance struct {
	lock *storeFileLock
}

// AcquireLauncherInstance prevents two launchers from independently managing
// the same registry, database pools, and child-process lifecycle. When wait is
// true it is a self-restart child and may wait for the old process to exit.
func AcquireLauncherInstance(wait bool) (*LauncherInstance, error) {
	store, err := NewStore()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(filepath.Dir(store.path), instanceLockFileName)
	deadline := time.Now()
	if wait {
		deadline = deadline.Add(instanceWaitTimeout)
	}
	for {
		lock, acquired, lockErr := tryAcquireStoreFileLock(path)
		if lockErr != nil {
			return nil, fmt.Errorf("launcher instance lock: %w", lockErr)
		}
		if acquired {
			return &LauncherInstance{lock: lock}, nil
		}
		if !wait || time.Now().After(deadline) {
			return nil, errors.New("launcher уже запущен для этого пользователя")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Release releases the singleton lease. Process exit also releases it.
func (i *LauncherInstance) Release() error {
	if i == nil || i.lock == nil {
		return nil
	}
	err := i.lock.Unlock()
	i.lock = nil
	return err
}
