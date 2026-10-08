package selfupdate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/installtest"
)

func transactionInstallation(t *testing.T, version string) (string, StagedInfo) {
	t.Helper()
	// Каталог установки обязан быть приватным на ЛЮБОЙ ОС: обычный t.TempDir()
	// лежит в общем /tmp, и проверка приватности (та самая, ради которой всё
	// это писалось) законно его отвергает — тесты падали всегда, а выглядело
	// это как дефект продукта (#924).
	targetDir := installtest.PrivateInstallDir(t)
	stageDir, err := newStageDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{targetDir, stageDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	names := PackageBinaries()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(targetDir, name), []byte("old:"+name), 0o755); err != nil { //nolint:gosec // test executable fixture
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stageDir, name), []byte(version+":"+name), 0o755); err != nil { //nolint:gosec // test executable fixture
			t.Fatal(err)
		}
	}
	staged := StagedInfo{Tag: version, Dir: stageDir, Files: names, Verified: true}
	return targetDir, staged
}

// privateTargetDir — каталог установки, который проверка приватности признаёт
// своим. Раньше на не-Windows отдавал обычный t.TempDir(), то есть ровно тот
// случай, который продукт отвергает: тесты падали всегда (#924).
func privateTargetDir(t *testing.T) string {
	t.Helper()
	return installtest.PrivateInstallDir(t)
}

// syncedOutput — вывод дочернего теста, безопасный для чтения, пока exec ещё
// копирует пайпы: без мьютекса чтение в ожидании маркеров гоняет с cmd.Wait.
type syncedOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncedOutput) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Протокол родитель↔helper — файлы-маркеры в каталоге
// ONEBASE_TARGET_LOCK_PROTOCOL, каждая стадия означает ровно одно событие:
//
//	lease — helper захватил собственную OperationLease;
//	apply — helper вот-вот войдёт в OperationLease.Apply (попытка захвата
//	        target lock; лок в этот момент держит родитель);
//	done  — Apply вернулся: payload "applied_at=<unixnano> targetlock=<0|1>".
//
// Раньше helper публиковал "ready" ДО захвата lease, а родитель оценивал
// блокировку порогами 100/150 мс — задержка планировщика съедала запас и
// давала ложный отказ исправной блокировки (#1611). Теперь на пути
// корректности порогов нет: родитель отпускает лок по истечении щедрого
// грейса — это ограничитель зависания, а не доказательство. Доказательство
// ожидания прикладное: Apply обязан вернуться только ПОСЛЕ снятия лока,
// иначе helper не ждал на нём вовсе, и это ложный успех, а не зелёный тест.
func TestTargetLockSubprocessHelper(t *testing.T) {
	if os.Getenv("ONEBASE_TARGET_LOCK_HELPER") != "1" {
		return
	}
	targetDir := os.Getenv("ONEBASE_TARGET_LOCK_TARGET")
	proto := os.Getenv("ONEBASE_TARGET_LOCK_PROTOCOL")
	publish := func(name, payload string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(proto, name), []byte(payload), 0o600); err != nil { //nolint:gosec // G703: parent test passes a path created beneath t.TempDir through the helper environment
			t.Fatal(err)
		}
	}
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatalf("acquire operation lease: %v", err)
	}
	defer func() { _ = lease.Release() }()
	publish("lease", "lease")

	publish("apply", "apply")
	started := time.Now()
	applyErr := lease.Apply(StagedInfo{Verified: true}, targetDir)
	waited := time.Since(started)
	if applyErr == nil {
		t.Fatal("invalid helper apply unexpectedly succeeded")
	}
	// Структурное доказательство пути блокировки: у Apply с некорректным
	// пакетом target lock уже должен быть захвачен (см.
	// TestLegacyLeaseApplyBindsTargetLock). Пустой targetLock означал бы обход
	// лока или отказ до попытки его захвата.
	if lease.targetLock == nil {
		t.Fatalf("helper Apply bypassed the target-scoped lock (waited %s): %v", waited, applyErr)
	}
	publish("done", "targetlock=1")
}

func TestTargetLockSerializesApplyAcrossProfilesAndProcesses(t *testing.T) {
	if os.Getenv("ONEBASE_TARGET_LOCK_HELPER") == "1" {
		t.Skip("parent-only test")
	}
	isolatedHome(t)
	targetDir, _ := transactionInstallation(t, "new")
	lock, err := acquireTargetFileLock(filepath.Join(targetDir, targetOperationLockFileName))
	if err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_ = lock.Unlock()
		}
	}()
	otherHome := t.TempDir()
	proto := t.TempDir()
	output := &syncedOutput{}
	done := make(chan error, 1)
	// diagnose прикладывает вывод helper к любому отказу: раньше печаталась
	// только ошибка cmd.Wait(), и причина падения helper была неизвестна.
	diagnose := func(format string, args ...any) {
		t.Helper()
		t.Fatalf("%s\nвывод helper:\n%s", fmt.Sprintf(format, args...), output.String())
	}
	waitMarker := func(name string, timeout time.Duration) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for {
			if _, err := os.Stat(filepath.Join(proto, name)); err == nil {
				return
			}
			select {
			case err := <-done:
				diagnose("helper exited before the %s marker: %v", name, err)
			case <-time.After(10 * time.Millisecond):
			}
			if time.Now().After(deadline) {
				diagnose("helper did not publish the %s marker in %s", name, timeout)
			}
		}
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestTargetLockSubprocessHelper$") //nolint:gosec // G204: execute this exact Go test binary with a fixed helper selector
	cmd.Env = append(os.Environ(),
		"ONEBASE_TARGET_LOCK_HELPER=1",
		"ONEBASE_TARGET_LOCK_TARGET="+targetDir,
		"ONEBASE_TARGET_LOCK_PROTOCOL="+proto,
		"HOME="+otherHome,
		"USERPROFILE="+otherHome,
	)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()
	// Дочерний процесс не должен пережить родительский тест: при любом выходе
	// завершаем и вычитываем его, чтобы он не удержал захваченные локи.
	running := true
	t.Cleanup(func() {
		if running {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})

	// Грейсы ниже — ограничители зависания: щедрый запас планировщику, чтобы
	// ложный отказ не вернулся ни на одной ОС.
	waitMarker("lease", 30*time.Second)
	waitMarker("apply", 30*time.Second)
	// Пока лок удерживается, исправный helper заблокирован в Apply и не может
	// ни выйти, ни закончить его: ранний маркер done или выход процесса —
	// обход лока или отказ до блокировки.
	graceDeadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(proto, "done")); err == nil {
			running = false
			diagnose("helper finished Apply while the target lock was held")
		}
		select {
		case err := <-done:
			running = false
			diagnose("helper exited while the target lock was held: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
		if time.Now().After(graceDeadline) {
			break
		}
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	locked = false
	select {
	case err := <-done:
		running = false
		if err != nil {
			diagnose("helper did not proceed after target lock release: %v", err)
		}
	case <-time.After(30 * time.Second):
		diagnose("helper did not proceed after target lock release: helper still running")
	}
	payload, err := os.ReadFile(filepath.Join(proto, "done")) //nolint:gosec // G703: protocol dir is test-owned beneath t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "targetlock=1" {
		diagnose("helper Apply did not bind the target-scoped lock: done payload %q", payload)
	}
}

func TestDurableAuthorityTransitionsUseRenamePrimitive(t *testing.T) {
	isolatedHome(t)
	updates, err := UpdatesDir()
	if err != nil {
		t.Fatal(err)
	}
	original := durableRename
	type renameCall struct {
		source, destination string
		replace             bool
	}
	var calls []renameCall
	durableRename = func(source, destination string, replace bool) error {
		calls = append(calls, renameCall{source: source, destination: destination, replace: replace})
		return platformDurableRename(source, destination, replace)
	}
	t.Cleanup(func() { durableRename = original })

	if err := writeFile(bytes.NewReader([]byte("state")), filepath.Join(updates, "authority.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || !calls[0].replace || filepath.Base(calls[0].destination) != "authority.json" {
		t.Fatalf("authority publication did not use durable replace: %+v", calls)
	}
}

func TestTargetMarkerLostAcknowledgementRecoversBeforeJournal(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := transactionInstallation(t, "new")
	original := durableRename
	injected := false
	durableRename = func(source, destination string, replace bool) error {
		err := platformDurableRename(source, destination, replace)
		if err == nil && !injected && filepath.Base(destination) == targetPendingFileName {
			injected = true
			return errors.New("injected lost acknowledgement")
		}
		return err
	}
	t.Cleanup(func() { durableRename = original })
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	if err := lease.ApplyWithRollbackState(staged, targetDir, "old-tag"); err == nil {
		t.Fatal("lost marker acknowledgement was ignored")
	}
	durableRename = original
	assertTransactionSet(t, targetDir, staged.Files, "old")
	if _, err := os.Stat(targetPendingPath(targetDir)); !os.IsNotExist(err) {
		t.Fatalf("pre-journal marker survived deterministic recovery: %v", err)
	}
	updates, err := UpdatesDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(updates, updateJournalFileName)); !os.IsNotExist(err) {
		t.Fatalf("journal was published after marker acknowledgement failed: %v", err)
	}
}

func TestRecoverFailsClosedForPendingTransactionOwnedByAnotherProfile(t *testing.T) {
	isolatedHome(t)
	targetDir, _ := transactionInstallation(t, "new")
	canonical, err := CanonicalTargetDir(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	pending := targetPendingTransaction{
		Version:   updateTransactionVersion,
		ID:        updateTransactionPrefix + "foreign",
		TargetDir: canonical,
		Updates:   filepath.Join(t.TempDir(), "foreign-updates"),
	}
	data, err := json.Marshal(pending)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPendingPath(canonical), data, 0o666); err != nil {
		t.Fatal(err)
	}
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	if err := lease.Recover(targetDir); err == nil || !strings.Contains(err.Error(), "another profile") {
		t.Fatalf("foreign pending owner did not fail closed: %v", err)
	}
	if _, err := os.Stat(targetPendingPath(canonical)); err != nil {
		t.Fatalf("foreign marker was modified: %v", err)
	}
}

func TestRecoverUnsupportedTargetWithoutAuthorityIsNoop(t *testing.T) {
	isolatedHome(t)
	targetDir := unsupportedRecoveryTarget(t)
	if CanSafelyUpdateBinaryDir(targetDir) {
		t.Fatal("test target unexpectedly supports self-update coordination")
	}
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	recovered, err := lease.RecoverWithResult(targetDir)
	if err != nil {
		t.Fatalf("unsupported installation without recovery authority was rejected: %v", err)
	}
	if recovered {
		t.Fatal("recovery reported a transaction for an installation without authority markers")
	}
	if lease.targetIntent != nil || lease.targetLock != nil || lease.targetDir != "" {
		t.Fatal("unsupported installation was bound to the writer-lock protocol")
	}
	for _, name := range []string{targetOperationLockFileName, targetOperationIntentLockFileName, targetPendingFileName} {
		if _, statErr := os.Lstat(filepath.Join(targetDir, name)); !os.IsNotExist(statErr) {
			t.Fatalf("unsupported recovery created %s: %v", name, statErr)
		}
	}
}

func TestRecoverUnsupportedTargetWithMarkerFailsClosed(t *testing.T) {
	isolatedHome(t)
	targetDir := unsupportedRecoveryTarget(t)
	markerPath := targetPendingPath(targetDir)
	marker := []byte("{}")
	if err := os.WriteFile(markerPath, marker, 0o600); err != nil {
		t.Fatal(err)
	}
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	if recovered, err := lease.RecoverWithResult(targetDir); err == nil || recovered {
		t.Fatalf("unsupported installation marker was ignored: recovered=%v err=%v", recovered, err)
	}
	got, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("pending marker was removed: %v", err)
	}
	if !bytes.Equal(got, marker) {
		t.Fatalf("pending marker was modified: got %q, want %q", got, marker)
	}
}

func TestRecoverUnsupportedTargetWithProfileJournalFailsClosed(t *testing.T) {
	isolatedHome(t)
	targetDir := unsupportedRecoveryTarget(t)
	stageDir := t.TempDir()
	names := PackageBinaries()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(targetDir, name), []byte("old:"+name), 0o755); err != nil { //nolint:gosec // test fixture
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stageDir, name), []byte("new:"+name), 0o755); err != nil { //nolint:gosec // test fixture
			t.Fatal(err)
		}
	}
	staged := StagedInfo{Tag: "new", Dir: stageDir, Files: names, Verified: true}
	tx, _, err := prepareUpdateTransaction("apply", targetDir, stageDir, targetDir, names)
	if err != nil {
		t.Fatal(err)
	}
	tx.Staged = &staged
	if err := writeUpdateJournal(tx); err != nil {
		t.Fatal(err)
	}
	updates, err := UpdatesDir()
	if err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(updates, updateJournalFileName)
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	if recovered, err := lease.RecoverWithResult(targetDir); err == nil || recovered || !strings.Contains(err.Error(), "no target ownership marker") {
		t.Fatalf("profile journal without target authority was accepted: recovered=%v err=%v", recovered, err)
	}
	if _, err := os.Stat(journalPath); err != nil {
		t.Fatalf("untrusted profile journal was modified: %v", err)
	}
}

func TestTargetLockIsStableAndSerializesIndependentUserLeases(t *testing.T) {
	isolatedHome(t)
	targetDir, _ := transactionInstallation(t, "new")
	first, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	if err := first.bindTarget(targetDir); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(targetDir, targetOperationLockFileName)
	before, err := os.Lstat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Release() }()
	if err := second.bindTarget(targetDir); err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("target lock inode was replaced between independent leases")
	}
}

func TestLegacyLeaseApplyBindsTargetLock(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := transactionInstallation(t, "new")
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	// A missing tag fails after the target lock should already be acquired,
	// making this a mutation-free assertion about the legacy public path.
	staged.Tag = ""
	if err := lease.Apply(staged, targetDir); err == nil {
		t.Fatal("invalid legacy Apply unexpectedly succeeded")
	}
	if lease.targetLock == nil || lease.targetDir == "" {
		t.Fatal("legacy OperationLease.Apply bypassed the target-scoped lock")
	}
	if _, err := os.Lstat(filepath.Join(targetDir, targetOperationLockFileName)); err != nil {
		t.Fatalf("legacy Apply did not create/open the stable target lock: %v", err)
	}
}

func TestLeaseUsesCanonicalLockedTargetAfterAliasRetarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require Windows developer mode")
	}
	isolatedHome(t)
	targetA, staged := transactionInstallation(t, "new")
	targetB, _ := transactionInstallation(t, "other")
	alias := filepath.Join(t.TempDir(), "install")
	if err := os.Symlink(targetA, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	if err := lease.bindTarget(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetB, alias); err != nil {
		t.Fatal(err)
	}
	if err := lease.Apply(staged, alias); err == nil {
		t.Fatal("retargeted installation alias was accepted after locking a different target")
	}
	assertTransactionSet(t, targetA, staged.Files, "old")
	assertTransactionSet(t, targetB, staged.Files, "old")
}

func TestApplyRejectsProtocolLockNameWithoutReplacingLockInode(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := transactionInstallation(t, "new")
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	if err := lease.bindTarget(targetDir); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(targetDir, targetOperationLockFileName)
	before, err := os.Lstat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged.Dir, targetOperationLockFileName), []byte("attacker"), 0o600); err != nil {
		t.Fatal(err)
	}
	staged.Files = append(staged.Files, targetOperationLockFileName)
	if err := lease.Apply(staged, targetDir); err == nil {
		t.Fatal("protocol lock name was accepted as a package binary")
	}
	after, err := os.Lstat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("rejected package replaced the held target lock inode")
	}
}

func TestTargetCoordinationModeMatchesWritableDirectoryClasses(t *testing.T) {
	tests := []struct {
		dir, want os.FileMode
	}{
		{dir: 0o755, want: 0o600},
		{dir: 0o775, want: 0o660},
		{dir: 0o777, want: 0o666},
		{dir: 0o750, want: 0o600},
	}
	for _, test := range tests {
		if got := targetCoordinationMode(test.dir); got != test.want {
			t.Errorf("targetCoordinationMode(%#o) = %#o, want %#o", test.dir, got, test.want)
		}
	}
}

func TestConsumerGenerationAttestationRejectsProcessLoadedBeforeSwap(t *testing.T) {
	isolate := privateTargetDir(t)
	original := consumerBinaryVersion
	consumerBinaryVersion = func(string) (string, error) { return "new-generation", nil }
	t.Cleanup(func() { consumerBinaryVersion = original })

	lease, err := acquireConsumerLease(isolate, "old-loaded-generation")
	if lease != nil {
		_ = lease.Release()
		t.Fatal("consumer lease was granted to an old in-memory generation")
	}
	if !errors.Is(err, ErrConsumerGenerationChanged) {
		t.Fatalf("generation attestation error = %v, want ErrConsumerGenerationChanged", err)
	}
	if processConsumerState.lease != nil {
		t.Fatal("rejected generation remained registered as a process consumer")
	}
}

func TestConsumerRejectsPendingTargetBeforeJoiningReaderSet(t *testing.T) {
	targetDir := privateTargetDir(t)
	if err := os.WriteFile(targetPendingPath(targetDir), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	lease, err := AcquireConsumerLease(targetDir)
	if lease != nil {
		_ = lease.Release()
		t.Fatal("pending target granted a consumer lease")
	}
	if !errors.Is(err, ErrPendingBinaryTransaction) {
		t.Fatalf("pending target error = %v, want ErrPendingBinaryTransaction", err)
	}
}

func TestProcessWriterReservationRejectsConcurrentConsumerAcquisition(t *testing.T) {
	targetDir := privateTargetDir(t)
	canonical, err := CanonicalTargetDir(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := reserveProcessWriter(canonical); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = releaseProcessWriter(canonical) })
	lease, err := AcquireConsumerLease(targetDir)
	if lease != nil {
		_ = lease.Release()
		t.Fatal("consumer lease was granted while this process reserved a writer")
	}
	if err == nil || !strings.Contains(err.Error(), "writer") {
		t.Fatalf("consumer acquisition error = %v, want process writer rejection", err)
	}
}

func TestValidatedRollbackInfoClearsMissingSnapshot(t *testing.T) {
	isolate := t.TempDir()
	t.Setenv("HOME", isolate)
	t.Setenv("USERPROFILE", isolate)
	targetDir, staged := transactionInstallation(t, "new")
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.ApplyWithRollbackState(staged, targetDir, "old-tag"); err != nil {
		_ = lease.Release()
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	info, err := ValidatedRollbackInfo(targetDir)
	if err != nil || info == nil || info.Tag != "old-tag" {
		t.Fatalf("valid rollback info = %+v, err = %v", info, err)
	}
	prev, err := PrevDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(prev, PackageBinaries()[0])); err != nil {
		t.Fatal(err)
	}
	info, err = ValidatedRollbackInfo(targetDir)
	if info != nil || err == nil {
		t.Fatalf("missing snapshot still advertised: info=%+v err=%v", info, err)
	}
	state, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Prev != nil {
		t.Fatalf("missing rollback snapshot remained advertised in state: %+v", state.Prev)
	}
}

func assertTransactionSet(t *testing.T, targetDir string, names []string, prefix string) {
	t.Helper()
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(targetDir, name)) //nolint:gosec // test-owned path
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if got, want := string(data), prefix+":"+name; got != want {
			t.Fatalf("mixed or wrong installation: %s = %q, want %q", name, got, want)
		}
	}
}

func installWithRecordedState(t *testing.T, targetDir string, staged StagedInfo, previousTag string) {
	t.Helper()
	if err := SaveState(State{Staged: &staged}); err != nil {
		t.Fatal(err)
	}
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
	}()
	if err := lease.ApplyWithRollbackState(staged, targetDir, previousTag); err != nil {
		t.Fatalf("ApplyWithRollbackState: %v", err)
	}
}

func crashOnceAt(t *testing.T, point string) {
	t.Helper()
	fired := false
	updateTransactionCutpoint = func(got string) error {
		if got == point && !fired {
			fired = true
			return errors.New("power lost")
		}
		return nil
	}
	t.Cleanup(func() { updateTransactionCutpoint = nil })
}

func TestApplyTransactionCrashRecoveryNeverLeavesMixedSet(t *testing.T) {
	binaries := PackageBinaries()
	points := []struct {
		name      string
		committed bool
	}{
		{name: "apply:target-published"},
		{name: "apply:journal-published"},
		{name: "apply:prepared"},
		{name: "apply:replaced:" + binaries[0]},
		{name: "apply:committed", committed: true},
		{name: "apply:prev-published", committed: true},
		{name: "apply:journal-retired", committed: true},
	}
	if len(binaries) > 1 {
		points = append(points, struct {
			name      string
			committed bool
		}{name: "apply:replaced:" + binaries[1]})
	}
	for _, test := range points {
		t.Run(strings.ReplaceAll(test.name, ":", "_"), func(t *testing.T) {
			isolatedHome(t)
			targetDir, staged := transactionInstallation(t, "new")
			if err := SaveState(State{Staged: &staged}); err != nil {
				t.Fatal(err)
			}
			lease, err := AcquireOperationLease()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lease.Release() }()

			crashOnceAt(t, test.name)
			if err := lease.ApplyWithRollbackState(staged, targetDir, "old-tag"); err == nil {
				t.Fatalf("cut point %s did not interrupt Apply", test.name)
			}
			updateTransactionCutpoint = nil
			if err := lease.Recover(targetDir); err != nil {
				t.Fatalf("Recover after %s: %v", test.name, err)
			}
			state, err := LoadState()
			if err != nil {
				t.Fatal(err)
			}
			if test.committed {
				assertTransactionSet(t, targetDir, staged.Files, "new")
				if state.Prev == nil || state.Prev.Tag != "old-tag" || state.Staged != nil {
					t.Fatalf("committed recovery did not reconcile state: %+v", state)
				}
				if err := lease.ValidateRollbackSnapshot(targetDir); err != nil {
					t.Fatalf("committed recovery did not publish a complete rollback snapshot: %v", err)
				}
			} else {
				assertTransactionSet(t, targetDir, staged.Files, "old")
				if state.Prev != nil || state.Staged == nil || state.Staged.Tag != staged.Tag {
					t.Fatalf("pre-commit recovery did not restore state: %+v", state)
				}
			}
			updates, err := UpdatesDir()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(updates, updateJournalFileName)); !os.IsNotExist(err) {
				t.Fatalf("journal survived completed recovery: %v", err)
			}
		})
	}
}

func TestApplyPublishedJournalRecoveryClearsUncapturedInvalidPrev(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := transactionInstallation(t, "new")
	if err := SaveState(State{
		Staged: &staged,
		Prev:   &RelInfo{Tag: "stale", TargetDir: t.TempDir()},
	}); err != nil {
		t.Fatal(err)
	}
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()

	crashOnceAt(t, "apply:journal-published")
	if err := lease.ApplyWithRollbackState(staged, targetDir, "old-tag"); err == nil {
		t.Fatal("simulated crash did not interrupt Apply")
	}
	updateTransactionCutpoint = nil
	if err := lease.Recover(targetDir); err != nil {
		t.Fatal(err)
	}
	assertTransactionSet(t, targetDir, staged.Files, "old")
	state, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Prev != nil || state.Staged == nil || state.Staged.Tag != staged.Tag {
		t.Fatalf("pre-commit recovery advertised an invalid rollback snapshot: %+v", state)
	}
}

func TestApplyRejectsInvalidJournalMetadataBeforeMutation(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := transactionInstallation(t, "new")
	staged.Tag = ""
	if err := SaveState(State{Staged: &staged}); err != nil {
		t.Fatal(err)
	}
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()

	if err := lease.ApplyWithRollbackState(staged, targetDir, "old-tag"); err == nil {
		t.Fatal("empty staged tag was accepted")
	}
	assertTransactionSet(t, targetDir, staged.Files, "old")
	updates, err := UpdatesDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(updates, updateJournalFileName)); !os.IsNotExist(err) {
		t.Fatalf("invalid transaction metadata published a journal: %v", err)
	}
}

func TestApplyPreparedRecoveryPreservesEarlierCompleteRollback(t *testing.T) {
	isolatedHome(t)
	targetDir, first := transactionInstallation(t, "current")
	installWithRecordedState(t, targetDir, first, "original-tag")

	stageDir, err := newStageDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	second := StagedInfo{Tag: "next", Dir: stageDir, Files: append([]string(nil), first.Files...), Verified: true}
	for _, name := range second.Files {
		if err := os.WriteFile(filepath.Join(stageDir, name), []byte("next:"+name), 0o755); err != nil { //nolint:gosec // test fixture
			t.Fatal(err)
		}
	}
	if _, err := UpdateState(func(state *State) error {
		state.Staged = &second
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	crashOnceAt(t, "apply:replaced:"+second.Files[0])
	if err := lease.ApplyWithRollbackState(second, targetDir, "current-tag"); err == nil {
		t.Fatal("simulated crash did not interrupt second apply")
	}
	updateTransactionCutpoint = nil
	if err := lease.Recover(targetDir); err != nil {
		t.Fatal(err)
	}
	assertTransactionSet(t, targetDir, first.Files, "current")
	state, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Prev == nil || state.Prev.Tag != "original-tag" || state.Staged == nil || state.Staged.Tag != second.Tag {
		t.Fatalf("earlier rollback or retryable stage was lost: %+v", state)
	}
	if err := lease.ValidateRollbackSnapshot(targetDir); err != nil {
		t.Fatalf("earlier rollback snapshot is no longer complete: %v", err)
	}

	crashOnceAt(t, "apply:old-prev-moved")
	if err := lease.ApplyWithRollbackState(second, targetDir, "current-tag"); err == nil {
		t.Fatal("simulated crash did not interrupt rollback-snapshot publication")
	}
	updateTransactionCutpoint = nil
	if err := lease.Recover(targetDir); err != nil {
		t.Fatalf("recover after moving the prior snapshot: %v", err)
	}
	assertTransactionSet(t, targetDir, second.Files, "next")
	state, err = LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Prev == nil || state.Prev.Tag != "current-tag" || state.Staged != nil {
		t.Fatalf("forward recovery did not publish the replacement snapshot and state: %+v", state)
	}
	if err := lease.ValidateRollbackSnapshot(targetDir); err != nil {
		t.Fatalf("replacement rollback snapshot is incomplete: %v", err)
	}
}

func TestRollbackTransactionCrashRecoveryNeverLeavesMixedSet(t *testing.T) {
	binaries := PackageBinaries()
	points := []struct {
		name      string
		committed bool
	}{
		{name: "rollback:target-published"},
		{name: "rollback:prepared"},
		{name: "rollback:replaced:" + binaries[0]},
		{name: "rollback:committed", committed: true},
		{name: "rollback:snapshot-consumed", committed: true},
		{name: "rollback:journal-retired", committed: true},
	}
	if len(binaries) > 1 {
		points = append(points, struct {
			name      string
			committed bool
		}{name: "rollback:replaced:" + binaries[1]})
	}
	for _, test := range points {
		t.Run(strings.ReplaceAll(test.name, ":", "_"), func(t *testing.T) {
			isolatedHome(t)
			targetDir, staged := transactionInstallation(t, "new")
			installWithRecordedState(t, targetDir, staged, "old-tag")
			lease, err := AcquireOperationLease()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lease.Release() }()
			crashOnceAt(t, test.name)
			if err := lease.RollbackPrev(targetDir); err == nil {
				t.Fatalf("cut point %s did not interrupt RollbackPrev", test.name)
			}
			updateTransactionCutpoint = nil
			if err := lease.Recover(targetDir); err != nil {
				t.Fatalf("Recover after %s: %v", test.name, err)
			}
			state, err := LoadState()
			if err != nil {
				t.Fatal(err)
			}
			if test.committed {
				assertTransactionSet(t, targetDir, staged.Files, "old")
				if state.Prev != nil {
					t.Fatalf("consumed rollback remains advertised: %+v", state.Prev)
				}
				if err := lease.ValidateRollbackSnapshot(targetDir); err == nil {
					t.Fatal("consumed rollback snapshot is still available")
				}
			} else {
				assertTransactionSet(t, targetDir, staged.Files, "new")
				if state.Prev == nil || state.Prev.Tag != "old-tag" {
					t.Fatalf("pre-commit rollback recovery lost Prev: %+v", state)
				}
				if err := lease.ValidateRollbackSnapshot(targetDir); err != nil {
					t.Fatalf("pre-commit rollback recovery damaged snapshot: %v", err)
				}
			}
		})
	}
}

func TestRollbackRejectsPartialSnapshotBeforeMutation(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := transactionInstallation(t, "new")
	installWithRecordedState(t, targetDir, staged, "old-tag")
	prev, err := PrevDir()
	if err != nil {
		t.Fatal(err)
	}
	if len(staged.Files) == 0 {
		t.Fatal("platform package has no binaries")
	}
	missing := staged.Files[len(staged.Files)-1]
	if err := os.Remove(filepath.Join(prev, missing)); err != nil {
		t.Fatal(err)
	}

	if err := RollbackPrev(targetDir); err == nil {
		t.Fatal("partial rollback snapshot was accepted")
	}
	assertTransactionSet(t, targetDir, staged.Files, "new")
	state, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Prev != nil {
		t.Fatalf("partial rollback snapshot remains advertised: %+v", state.Prev)
	}
	if _, err := os.Stat(prev); err != nil {
		t.Fatalf("failed rollback deleted recovery material: %v", err)
	}
}

func TestApplySnapshotConstructionFailureDoesNotMutateOrLosePriorRollback(t *testing.T) {
	isolatedHome(t)
	targetDir, first := transactionInstallation(t, "current")
	installWithRecordedState(t, targetDir, first, "original-tag")

	stageDir, err := newStageDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	second := StagedInfo{Tag: "next", Dir: stageDir, Files: append([]string(nil), first.Files...), Verified: true}
	for _, name := range second.Files {
		if err := os.WriteFile(filepath.Join(stageDir, name), []byte("next:"+name), 0o755); err != nil { //nolint:gosec // test fixture
			t.Fatal(err)
		}
	}
	if _, err := UpdateState(func(state *State) error {
		state.Staged = &second
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	updateSnapshotBeforeCopy = func(_, destination string) error {
		if filepath.Base(destination) == second.Files[len(second.Files)-1] && filepath.Base(filepath.Dir(destination)) == transactionUndoDir {
			return errors.New("injected snapshot write failure")
		}
		return nil
	}
	t.Cleanup(func() { updateSnapshotBeforeCopy = nil })

	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	if err := lease.ApplyWithRollbackState(second, targetDir, "current-tag"); err == nil {
		t.Fatal("snapshot construction error was ignored")
	}
	updateSnapshotBeforeCopy = nil
	assertTransactionSet(t, targetDir, first.Files, "current")
	state, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Prev == nil || state.Prev.Tag != "original-tag" || state.Staged == nil || state.Staged.Tag != second.Tag {
		t.Fatalf("snapshot failure changed durable state: %+v", state)
	}
	if err := lease.ValidateRollbackSnapshot(targetDir); err != nil {
		t.Fatalf("snapshot failure damaged earlier rollback material: %v", err)
	}
	updates, err := UpdatesDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(updates, updateJournalFileName)); !os.IsNotExist(err) {
		t.Fatalf("snapshot failure published a journal: %v", err)
	}
}

func TestCommittedApplyRetainsJournalUntilStateIsReconciled(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := transactionInstallation(t, "new")
	if err := SaveState(State{Staged: &staged}); err != nil {
		t.Fatal(err)
	}
	stateSaveBeforeWrite = func(_ string, state State) error {
		if state.Prev != nil && state.Prev.Tag == "old-tag" {
			return errors.New("injected state write failure")
		}
		return nil
	}
	t.Cleanup(func() { stateSaveBeforeWrite = nil })
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()

	if err := lease.ApplyWithRollbackState(staged, targetDir, "old-tag"); err == nil {
		t.Fatal("committed update reported success without durable state")
	}
	assertTransactionSet(t, targetDir, staged.Files, "new")
	updates, err := UpdatesDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(updates, updateJournalFileName)); err != nil {
		t.Fatalf("committed recovery material was deleted after state failure: %v", err)
	}

	stateSaveBeforeWrite = nil
	if err := lease.Recover(targetDir); err != nil {
		t.Fatal(err)
	}
	state, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Prev == nil || state.Prev.Tag != "old-tag" || state.Staged != nil {
		t.Fatalf("recovery did not reconcile committed state: %+v", state)
	}
	if err := lease.ValidateRollbackSnapshot(targetDir); err != nil {
		t.Fatalf("rollback snapshot was lost after state recovery: %v", err)
	}
	if _, err := os.Stat(filepath.Join(updates, updateJournalFileName)); !os.IsNotExist(err) {
		t.Fatalf("journal remains after state recovery: %v", err)
	}
}

func TestRecoveryPendingErrorIsTypedWhenSettlementCannotComplete(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := transactionInstallation(t, "new")
	if err := SaveState(State{Staged: &staged}); err != nil {
		t.Fatal(err)
	}
	stateSaveBeforeWrite = func(_ string, state State) error {
		if state.Prev != nil && state.Prev.Tag == "old-tag" {
			return errors.New("persistent state storage failure")
		}
		return nil
	}
	t.Cleanup(func() { stateSaveBeforeWrite = nil })
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()

	err = lease.ApplyWithRollbackState(staged, targetDir, "old-tag")
	if !RecoveryPending(err) {
		t.Fatalf("error = %v, want typed recovery-pending error", err)
	}
}

func TestCommittedRollbackRetainsJournalUntilStateIsCleared(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := transactionInstallation(t, "new")
	installWithRecordedState(t, targetDir, staged, "old-tag")
	stateSaveBeforeWrite = func(_ string, state State) error {
		if state.Prev == nil {
			return errors.New("injected state write failure")
		}
		return nil
	}
	t.Cleanup(func() { stateSaveBeforeWrite = nil })
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()

	if err := lease.RollbackPrev(targetDir); err == nil {
		t.Fatal("committed rollback reported success without clearing durable state")
	}
	assertTransactionSet(t, targetDir, staged.Files, "old")
	updates, err := UpdatesDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(updates, updateJournalFileName)); err != nil {
		t.Fatalf("rollback journal was deleted after state failure: %v", err)
	}

	stateSaveBeforeWrite = nil
	if err := lease.Recover(targetDir); err != nil {
		t.Fatal(err)
	}
	state, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Prev != nil {
		t.Fatalf("consumed rollback remains advertised after recovery: %+v", state.Prev)
	}
	if _, err := os.Stat(filepath.Join(updates, updateJournalFileName)); !os.IsNotExist(err) {
		t.Fatalf("rollback journal remains after recovery: %v", err)
	}
}

func TestCommittedRollbackRetainsJournalWhenConsumedSnapshotIsDamaged(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := transactionInstallation(t, "new")
	installWithRecordedState(t, targetDir, staged, "old-tag")
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()

	crashOnceAt(t, "rollback:snapshot-consumed")
	if err := lease.RollbackPrev(targetDir); err == nil {
		t.Fatal("simulated crash did not interrupt RollbackPrev")
	}
	updateTransactionCutpoint = nil
	tx, txDir, err := readUpdateJournal(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(txDir, transactionConsumedPrev, tx.Desired[0].Name)); err != nil {
		t.Fatal(err)
	}
	if err := lease.Recover(targetDir); err == nil {
		t.Fatal("damaged consumed snapshot was accepted")
	}
	assertTransactionSet(t, targetDir, staged.Files, "old")
	updates, err := UpdatesDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(updates, updateJournalFileName)); err != nil {
		t.Fatalf("journal was deleted after incomplete rollback recovery: %v", err)
	}
	state, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Prev != nil {
		t.Fatalf("damaged consumed snapshot remains advertised: %+v", state.Prev)
	}
}

func TestCompletedTransactionLocatorCleansOnlyDigestBoundDisplacedFile(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := transactionInstallation(t, "new")
	installWithRecordedState(t, targetDir, staged, "old-tag")
	updates, err := UpdatesDir()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(updates)
	if err != nil {
		t.Fatal(err)
	}
	_ = entries
	record, err := inspectRegularFile(filepath.Join(targetDir, staged.Files[0]))
	if err != nil {
		t.Fatal(err)
	}
	record.Name = staged.Files[0]
	txDir, err := os.MkdirTemp(updates, updateTransactionPrefix)
	if err != nil {
		t.Fatal(err)
	}
	canonicalTarget, err := CanonicalTargetDir(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	tx := updateTransaction{
		Version: updateTransactionVersion, ID: filepath.Base(txDir), Kind: "rollback", TargetDir: canonicalTarget,
		Desired: []updateTxnFile{record}, Undo: []updateTxnFile{record},
	}
	data, err := json.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(txDir, transactionCompletedFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	displaced := displacedTransactionPath(filepath.Join(targetDir, record.Name), record, tx.ID)
	payload, err := os.ReadFile(filepath.Join(targetDir, record.Name))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(displaced, payload, os.FileMode(record.Mode)); err != nil { //nolint:gosec // G703: displaced is digest-bound under the test-owned installation directory
		t.Fatal(err)
	}
	unrelated := filepath.Join(targetDir, record.Name+".update-unrelated")
	if err := os.WriteFile(unrelated, payload, 0o600); err != nil { //nolint:gosec // G703: unrelated is a fixed suffix under the test-owned installation directory
		t.Fatal(err)
	}
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	if err := lease.Recover(targetDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(displaced); !os.IsNotExist(err) {
		t.Fatalf("digest-bound displaced file survived cleanup: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("broad cleanup removed unrelated file: %v", err)
	}
}

func TestRecoverSkipsCompletedCleanupLocatorForAnotherInstallation(t *testing.T) {
	isolatedHome(t)
	targetA, stagedA := transactionInstallation(t, "new-a")
	targetB, stagedB := transactionInstallation(t, "new-b")
	updates, err := UpdatesDir()
	if err != nil {
		t.Fatal(err)
	}
	record, err := inspectRegularFile(filepath.Join(targetA, stagedA.Files[0]))
	if err != nil {
		t.Fatal(err)
	}
	record.Name = stagedA.Files[0]
	txDir, err := os.MkdirTemp(updates, updateTransactionPrefix)
	if err != nil {
		t.Fatal(err)
	}
	canonicalA, err := CanonicalTargetDir(targetA)
	if err != nil {
		t.Fatal(err)
	}
	tx := updateTransaction{
		Version: updateTransactionVersion, ID: filepath.Base(txDir), Kind: "rollback", TargetDir: canonicalA,
		Desired: []updateTxnFile{record}, Undo: []updateTxnFile{record},
	}
	data, err := json.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(txDir, transactionCompletedFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	if err := lease.Recover(targetB); err != nil {
		t.Fatalf("install A cleanup locator blocked install B: %v", err)
	}
	assertTransactionSet(t, targetB, stagedB.Files, "old")
	if _, err := os.Stat(filepath.Join(txDir, transactionCompletedFile)); err != nil {
		t.Fatalf("install B mutated install A cleanup locator: %v", err)
	}
}
