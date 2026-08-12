package selfupdate

import (
	"errors"
	"path/filepath"
	"sync"
)

const stageLockFileName = "stage.lock"

const targetOperationLockFileName = ".onebase-update.lock"
const targetOperationIntentLockFileName = ".onebase-update.intent.lock"

// stageMu complements the cross-process stage.lock for POSIX, where record
// locks are process-scoped. It also keeps concurrent goroutines from sharing a
// staging lifecycle while one of them downloads or applies files.
var stageMu sync.Mutex

type stageOperationLock struct {
	fileLock *stateFileLock
	released bool
}

// OperationLease serializes the complete update transaction across processes:
// stopping workloads, replacing binaries, verifying them, and committing the
// recovery state. Holding only the internal lock inside Apply is not enough —
// another launcher could otherwise begin a second transaction while the first
// one still owns stopped processes or an uncommitted rollback snapshot.
type OperationLease struct {
	lock              *stageOperationLock
	targetLock        *stateFileLock
	targetIntent      *stateFileLock
	suspendedConsumer *ConsumerLease
	targetDir         string
}

// AcquireOperationLease obtains the user-wide update-operation lease. Callers
// must Release it. The lease is intentionally blocking: update operations are
// rare, and waiting is safer than racing another process through a binary swap.
func AcquireOperationLease() (*OperationLease, error) {
	lock, err := acquireStageOperationLock()
	if err != nil {
		return nil, err
	}
	return &OperationLease{lock: lock}, nil
}

// Release gives up the operation lease. It is safe to call more than once.
func (l *OperationLease) Release() error {
	if l == nil || l.lock == nil {
		return nil
	}
	err := errors.Join(l.releaseTarget(), l.lock.Unlock())
	l.lock = nil
	return err
}

// ReleaseTargetReservation drops this operation's installation intent while
// retaining the per-profile stage lease. Orchestrators call it before
// restarting consumers after a mutation-free or fully rolled-back failure;
// child processes must be able to join the reader set before becoming ready.
func (l *OperationLease) ReleaseTargetReservation() error {
	if !l.valid() {
		return errors.New("selfupdate: operation lease is not held")
	}
	return l.releaseTarget()
}

func (l *OperationLease) valid() bool {
	return l != nil && l.lock != nil && !l.lock.released
}

// bindTarget serializes update authority in the installation itself. The
// per-user stage lock alone cannot protect a shared installation when two OS
// users have distinct home/update directories.
func (l *OperationLease) bindTarget(targetDir string) error {
	if err := l.ReserveTarget(targetDir); err != nil {
		return err
	}
	if l.targetLock != nil {
		return nil
	}
	lock, err := acquireTargetFileLock(filepath.Join(l.targetDir, targetOperationLockFileName))
	if err != nil {
		return err
	}
	l.targetLock = lock
	return nil
}

// ReserveTarget serializes writer intent without waiting for consumer readers.
// Orchestrators use it before stopping their service/base processes, then
// Recover/Apply upgrades to exclusive after those readers have exited.
func (l *OperationLease) ReserveTarget(targetDir string) error {
	if !l.valid() {
		return errors.New("selfupdate: operation lease is not held")
	}
	canonical, err := CanonicalTargetDir(targetDir)
	if err != nil {
		return err
	}
	if err := validatePlainDirectory(canonical); err != nil {
		return err
	}
	if l.targetIntent != nil {
		if l.targetDir != canonical {
			return errors.New("selfupdate: operation lease is already bound to another installation")
		}
		return nil
	}
	if err := reserveProcessWriter(canonical); err != nil {
		return err
	}
	// Intent lives on a separate stable inode. POSIX fcntl record locks are
	// process-owned and closing the consumer descriptor would otherwise also
	// drop an intent lock held through another descriptor for the same inode.
	lockPath := filepath.Join(canonical, targetOperationIntentLockFileName)
	intent, err := acquireTargetIntentFileLock(lockPath)
	if err != nil {
		return errors.Join(err, releaseProcessWriter(canonical))
	}
	consumer, err := suspendProcessConsumer(canonical)
	if err != nil {
		return errors.Join(err, intent.Unlock(), releaseProcessWriter(canonical))
	}
	l.targetIntent = intent
	l.suspendedConsumer = consumer
	l.targetDir = canonical
	return nil
}

func (l *OperationLease) releaseTarget() error {
	if l == nil || (l.targetLock == nil && l.targetIntent == nil) {
		return nil
	}
	var err error
	if l.targetLock != nil {
		err = l.targetLock.Unlock()
		l.targetLock = nil
	}
	err = errors.Join(err, resumeProcessConsumer(l.suspendedConsumer))
	l.suspendedConsumer = nil
	if l.targetIntent != nil {
		err = errors.Join(err, l.targetIntent.Unlock())
		l.targetIntent = nil
	}
	err = errors.Join(err, releaseProcessWriter(l.targetDir))
	l.targetDir = ""
	return err
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
