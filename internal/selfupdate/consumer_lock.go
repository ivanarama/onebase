package selfupdate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/ivantit66/onebase/internal/version"
)

const EnvBinaryPendingEntry = "ONEBASE_BINARY_PENDING_AT_ENTRY"

var (
	ErrPendingBinaryTransaction  = errors.New("selfupdate: installation has a pending binary transaction")
	ErrConsumerGenerationChanged = errors.New("selfupdate: installed binary generation changed while this process was starting")
	consumerBinaryVersion        = BinaryVersion
	// currentBinaryPath — шов для тестов: они подменяют «текущий бинарь»
	// заглушкой, не пересобирая себя.
	currentBinaryPath = BinaryPath
)

var processConsumerState struct {
	sync.Mutex
	lease        *ConsumerLease
	acquiring    bool
	writerTarget string
}

// ConsumerLease pins one installed binary generation for a process lifetime.
// Updaters take an intent lock, suspend their own reader, then wait for every
// other process reader before obtaining the exclusive target lock.
type ConsumerLease struct {
	targetDir       string
	expectedVersion string
	lock            *stateFileLock
	released        bool
	suspended       bool
}

func AcquireBinaryConsumerLease() (*ConsumerLease, error) {
	targetDir, err := BinaryDir()
	if err != nil {
		return nil, err
	}
	return acquireConsumerLease(targetDir, version.String())
}

func AcquireConsumerLease(targetDir string) (*ConsumerLease, error) {
	return acquireConsumerLease(targetDir, "")
}

func acquireConsumerLease(targetDir, expectedVersion string) (*ConsumerLease, error) {
	canonical, err := CanonicalTargetDir(targetDir)
	if err != nil {
		return nil, err
	}
	if err := validatePlainDirectory(canonical); err != nil {
		return nil, err
	}
	processConsumerState.Lock()
	if processConsumerState.writerTarget != "" {
		processConsumerState.Unlock()
		return nil, errors.New("selfupdate: process update writer is reserved; consumer lease cannot be acquired")
	}
	if processConsumerState.acquiring || (processConsumerState.lease != nil && !processConsumerState.lease.released) {
		processConsumerState.Unlock()
		return nil, errors.New("selfupdate: process already holds or is acquiring a binary consumer lease")
	}
	processConsumerState.acquiring = true
	processConsumerState.Unlock()
	registered := false
	defer func() {
		if registered {
			return
		}
		processConsumerState.Lock()
		processConsumerState.acquiring = false
		processConsumerState.Unlock()
	}()
	// Pass through the intent gate before joining the reader set. Once an
	// updater owns intent, no new consumer may slip in between orchestration's
	// stop-all pass and its exclusive binary lock.
	intent, err := acquireTargetReadFileLock(filepath.Join(canonical, targetOperationIntentLockFileName))
	if err != nil {
		return nil, err
	}
	lock, err := acquireTargetReadFileLock(filepath.Join(canonical, targetOperationLockFileName))
	if err != nil {
		return nil, errors.Join(err, intent.Unlock())
	}
	if _, err := os.Lstat(targetPendingPath(canonical)); err == nil {
		return nil, errors.Join(ErrPendingBinaryTransaction, lock.Unlock(), intent.Unlock())
	} else if !os.IsNotExist(err) {
		return nil, errors.Join(err, lock.Unlock(), intent.Unlock())
	}
	if expectedVersion != "" {
		// Сверяем поколение ТЕКУЩЕГО исполняемого файла, а не файла с жёстко
		// зашитым именем в каталоге: бинарь, названный иначе (сборка
		// ob-2026-08-13.exe, `go build -o my-onebase`), проверял бы
		// несуществующий onebase.exe и не выполнял НИ ОДНОЙ команды (#831).
		self, pathErr := currentBinaryPath()
		if pathErr != nil {
			return nil, errors.Join(pathErr, lock.Unlock(), intent.Unlock())
		}
		got, versionErr := consumerBinaryVersion(self)
		if versionErr != nil {
			return nil, errors.Join(fmt.Errorf("selfupdate: verify installed binary generation: %w", versionErr), lock.Unlock(), intent.Unlock())
		}
		if got != expectedVersion {
			return nil, errors.Join(fmt.Errorf("%w: running %q, installed %q", ErrConsumerGenerationChanged, expectedVersion, got), lock.Unlock(), intent.Unlock())
		}
	}
	if err := intent.Unlock(); err != nil {
		return nil, errors.Join(err, lock.Unlock())
	}
	lease := &ConsumerLease{targetDir: canonical, expectedVersion: expectedVersion, lock: lock}
	processConsumerState.Lock()
	defer processConsumerState.Unlock()
	processConsumerState.acquiring = false
	processConsumerState.lease = lease
	registered = true
	return lease, nil
}

func reserveProcessWriter(targetDir string) error {
	processConsumerState.Lock()
	defer processConsumerState.Unlock()
	if processConsumerState.acquiring {
		return errors.New("selfupdate: binary consumer lease acquisition is in progress")
	}
	if processConsumerState.writerTarget != "" {
		if processConsumerState.writerTarget == targetDir {
			return nil
		}
		return errors.New("selfupdate: process update writer is already reserved for another installation")
	}
	processConsumerState.writerTarget = targetDir
	return nil
}

func releaseProcessWriter(targetDir string) error {
	processConsumerState.Lock()
	defer processConsumerState.Unlock()
	if processConsumerState.writerTarget == "" {
		return nil
	}
	if processConsumerState.writerTarget != targetDir {
		return errors.New("selfupdate: process update writer target changed unexpectedly")
	}
	processConsumerState.writerTarget = ""
	return nil
}

// AcquireConsumerLeaseIfWritable protects installations this process can
// actually update. Read-only system installations remain launchable by
// ordinary users; their update path is rejected before any mutation because
// that same user cannot write the binary directory.
func AcquireConsumerLeaseIfWritable(targetDir string) (*ConsumerLease, error) {
	if !CanSafelyUpdateBinaryDir(targetDir) {
		return nil, nil
	}
	return AcquireConsumerLease(targetDir)
}

func AcquireBinaryConsumerLeaseIfWritable() (*ConsumerLease, error) {
	targetDir, err := BinaryDir()
	if err != nil {
		return nil, err
	}
	if !CanSafelyUpdateBinaryDir(targetDir) {
		return nil, nil
	}
	return AcquireBinaryConsumerLease()
}

func (l *ConsumerLease) Release() error {
	if l == nil {
		return nil
	}
	processConsumerState.Lock()
	defer processConsumerState.Unlock()
	if l.released {
		return nil
	}
	l.released = true
	if processConsumerState.lease == l {
		processConsumerState.lease = nil
	}
	if l.lock == nil {
		return nil
	}
	err := l.lock.Unlock()
	l.lock = nil
	return err
}

func suspendProcessConsumer(targetDir string) (*ConsumerLease, error) {
	processConsumerState.Lock()
	defer processConsumerState.Unlock()
	consumer := processConsumerState.lease
	if consumer == nil || consumer.released || consumer.targetDir != targetDir {
		return nil, nil
	}
	if consumer.suspended || consumer.lock == nil {
		return nil, errors.New("selfupdate: process binary consumer lease is already suspended")
	}
	if err := consumer.lock.Unlock(); err != nil {
		return nil, err
	}
	consumer.lock = nil
	consumer.suspended = true
	return consumer, nil
}

func resumeProcessConsumer(consumer *ConsumerLease) error {
	if consumer == nil {
		return nil
	}
	processConsumerState.Lock()
	defer processConsumerState.Unlock()
	if consumer.released || !consumer.suspended {
		return nil
	}
	lock, err := acquireTargetReadFileLock(filepath.Join(consumer.targetDir, targetOperationLockFileName))
	if err != nil {
		return err
	}
	if consumer.expectedVersion != "" {
		self, pathErr := currentBinaryPath()
		if pathErr != nil {
			return pathErr
		}
		got, versionErr := consumerBinaryVersion(self)
		if versionErr != nil {
			return errors.Join(versionErr, lock.Unlock())
		}
		if got != consumer.expectedVersion {
			return errors.Join(fmt.Errorf("%w: running %q, installed %q", ErrConsumerGenerationChanged, consumer.expectedVersion, got), lock.Unlock())
		}
	}
	consumer.lock = lock
	consumer.suspended = false
	return nil
}
