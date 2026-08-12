package selfupdate

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func transactionInstallation(t *testing.T, version string) (string, StagedInfo) {
	t.Helper()
	base := t.TempDir()
	if root := windowsTestPrivateInstallRoot(); root != "" {
		privateBase, err := os.MkdirTemp(root, "onebase-selfupdate-test-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(privateBase) })
		base = privateBase
	}
	targetDir := filepath.Join(base, "bin")
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

func privateTargetDir(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	if root := windowsTestPrivateInstallRoot(); root != "" {
		var err error
		base, err = os.MkdirTemp(root, "onebase-selfupdate-test-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(base) })
	}
	return base
}

func TestTargetLockSubprocessHelper(t *testing.T) {
	if os.Getenv("ONEBASE_TARGET_LOCK_HELPER") != "1" {
		return
	}
	targetDir := os.Getenv("ONEBASE_TARGET_LOCK_TARGET")
	ready := os.Getenv("ONEBASE_TARGET_LOCK_READY")
	if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil { //nolint:gosec // G703: parent test passes a path created beneath t.TempDir through the helper environment
		t.Fatal(err)
	}
	lease, err := AcquireOperationLease()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	started := time.Now()
	err = lease.Apply(StagedInfo{Verified: true}, targetDir)
	if err == nil {
		t.Fatal("invalid helper apply unexpectedly succeeded")
	}
	if time.Since(started) < 100*time.Millisecond {
		t.Fatal("helper Apply did not wait for the target-scoped lock")
	}
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
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestTargetLockSubprocessHelper$") //nolint:gosec // G204: execute this exact Go test binary with a fixed helper selector
	cmd.Env = append(os.Environ(),
		"ONEBASE_TARGET_LOCK_HELPER=1",
		"ONEBASE_TARGET_LOCK_TARGET="+targetDir,
		"ONEBASE_TARGET_LOCK_READY="+ready,
		"HOME="+otherHome,
		"USERPROFILE="+otherHome,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not reach target lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case err := <-done:
		t.Fatalf("helper bypassed target lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	locked = false
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not proceed after target lock release")
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
