package selfupdate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/fsmode"
	oblog "github.com/ivantit66/onebase/internal/logging"
)

const (
	updateTransactionVersion = 1
	updateJournalFileName    = "transaction.json"
	updateCommitFileName     = "transaction.commit"
	updateTransactionPrefix  = ".transaction-"
	updateTransactionBuild   = ".transaction-build-"
	prevSnapshotFileName     = ".snapshot.json"
	transactionDesiredDir    = "desired"
	transactionUndoDir       = "undo"
	transactionObsoletePrev  = "obsolete-prev"
	transactionConsumedPrev  = "consumed-prev"
	transactionCompletedFile = "completed-transaction.json"
	transactionRetiredCommit = "completed-transaction.commit"
	orphanCommitTombstone    = "transaction.commit.retired"
	targetPendingFileName    = ".onebase-update.pending.json"
	maxTransactionJSON       = 1 << 20
)

type updateTransaction struct {
	Version     int             `json:"version"`
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	TargetDir   string          `json:"target_dir"`
	PreviousTag string          `json:"previous_tag,omitempty"`
	Staged      *StagedInfo     `json:"staged,omitempty"`
	PriorPrev   *RelInfo        `json:"prior_prev,omitempty"`
	Desired     []updateTxnFile `json:"desired"`
	Undo        []updateTxnFile `json:"undo"`
}

type updateTxnFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
}

type prevSnapshotManifest struct {
	Version   int             `json:"version"`
	TargetDir string          `json:"target_dir"`
	Files     []updateTxnFile `json:"files"`
}

type targetPendingTransaction struct {
	Version   int    `json:"version"`
	ID        string `json:"id"`
	TargetDir string `json:"target_dir"`
	Updates   string `json:"updates_dir"`
}

var errNoUpdateTransaction = errors.New("selfupdate: update transaction is not pending")

// ErrRecoveredGenerationChanged means recovery settled a transaction that
// predated the requested operation. The caller must restart from the installed
// binary before making another update decision with code/state from the old
// generation.
var ErrRecoveredGenerationChanged = errors.New("selfupdate: interrupted update was recovered; restart from the installed binary")

// RecoveryPendingError means the operation could not prove and restore a
// complete installed binary set. Callers must keep workloads stopped: starting
// them could execute different binaries from different releases.
type RecoveryPendingError struct {
	err error
}

func (e *RecoveryPendingError) Error() string {
	return fmt.Sprintf("selfupdate: durable binary recovery is still pending: %v", e.err)
}

func (e *RecoveryPendingError) Unwrap() error { return e.err }

// RecoveryPending reports whether an operation error requires callers to keep
// services and managed processes stopped until a later Recover succeeds.
func RecoveryPending(err error) bool {
	var pending *RecoveryPendingError
	return errors.As(err, &pending)
}

// NewRecoveryPendingError wraps a cause for orchestration seams and adapters
// that need to preserve the keep-workloads-stopped contract.
func NewRecoveryPendingError(err error) error {
	if err == nil {
		return nil
	}
	return &RecoveryPendingError{err: err}
}

// updateTransactionCutpoint is a test-only crash seam. Production leaves it
// nil. Returning an error deliberately leaves the durable journal and payloads
// untouched, exactly as abrupt process termination would.
var updateTransactionCutpoint func(string) error
var updateSnapshotBeforeCopy func(source, destination string) error

type transactionCutpointError struct {
	point string
	err   error
}

func (e *transactionCutpointError) Error() string {
	return fmt.Sprintf("selfupdate: simulated crash at %s: %v", e.point, e.err)
}
func (e *transactionCutpointError) Unwrap() error { return e.err }

func transactionCut(point string) error {
	if updateTransactionCutpoint == nil {
		return nil
	}
	if err := updateTransactionCutpoint(point); err != nil {
		return &transactionCutpointError{point: point, err: err}
	}
	return nil
}

// Recover completes or rolls back a transaction interrupted by process or
// machine failure. The expected target is mandatory: a damaged journal in the
// per-user updates directory must never authorize writes to an unrelated path.
func (l *OperationLease) Recover(targetDir string) error {
	_, err := l.RecoverWithResult(targetDir)
	return err
}

// RecoverWithResult reports whether a target-scoped pending transaction was
// settled. A running launcher must restart after true: recovery may have
// replaced its on-disk executable with the opposite transaction generation.
func (l *OperationLease) RecoverWithResult(targetDir string) (bool, error) {
	if !l.valid() {
		return false, errors.New("selfupdate: operation lease is not held")
	}
	canonical, err := CanonicalTargetDir(targetDir)
	if err != nil {
		return false, err
	}
	if err := validatePlainDirectory(canonical); err != nil {
		return false, err
	}

	// Inspect durable recovery evidence before requiring the installation to
	// participate in the writer-lock protocol. A shared/system installation is
	// intentionally not self-updatable, but it must remain launchable when no
	// update transaction ever started there.
	//
	// Both proofs are required. A target marker means binary mutation may have
	// begun; a profile journal without that marker is damaged authority and must
	// remain fail-closed. Only their proven absence permits an unsupported
	// installation to skip recovery.
	hadPending, err := targetPendingExists(canonical)
	if err != nil {
		return false, err
	}
	if !hadPending {
		if err := validateNoTargetAuthority(canonical); err != nil {
			return false, err
		}
		if err := ValidateBinaryUpdateTarget(canonical); err != nil {
			// Recheck after the capability probe. The per-profile operation lease
			// excludes our own writers; never treat unreadable evidence as absence.
			if pending, recheckErr := targetPendingExists(canonical); recheckErr != nil {
				return false, recheckErr
			} else if pending {
				return false, fmt.Errorf("selfupdate: pending binary update cannot be recovered: %w", err)
			}
			return false, nil
		}
	}

	if err := l.ReserveTarget(canonical); err != nil {
		return false, err
	}
	// Repeat the marker check under installation-scoped writer intent. A
	// transaction from another profile may have appeared during the read-only
	// preflight; never act on it without the target reservation held.
	hadPending, err = targetPendingExists(l.targetDir)
	if err != nil {
		return false, err
	}
	// With no target authority marker, recovery only cleans private orphan
	// state. Do not wait for long-lived consumers merely to prove that absence.
	// Intent still excludes a concurrent writer throughout the check/cleanup.
	if hadPending {
		if err := l.bindTarget(l.targetDir); err != nil {
			return false, err
		}
	}
	return hadPending, recoverUpdateTransactionLocked(l.targetDir)
}

func targetPendingExists(targetDir string) (bool, error) {
	_, err := os.Lstat(targetPendingPath(targetDir))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func recoverUpdateTransactionLockedWithResult(expectedTarget string) (bool, error) {
	hadPending, err := targetPendingExists(expectedTarget)
	if err != nil {
		return false, err
	}
	return hadPending, recoverUpdateTransactionLocked(expectedTarget)
}

// ValidateRollbackSnapshot verifies that State.Prev, if advertised by a
// caller, can only refer to a complete snapshot for this installation.
func (l *OperationLease) ValidateRollbackSnapshot(targetDir string) error {
	if !l.valid() {
		return errors.New("selfupdate: operation lease is not held")
	}
	if err := l.bindTarget(targetDir); err != nil {
		return err
	}
	targetDir = l.targetDir
	prev, err := PrevDir()
	if err != nil {
		return err
	}
	_, err = validatePrevSnapshot(prev, targetDir, nil)
	return err
}

func runApplyTransaction(staged StagedInfo, targetDir string, names []string, previousTag string) error {
	tx, txDir, err := prepareUpdateTransaction("apply", targetDir, staged.Dir, targetDir, names)
	if err != nil {
		return err
	}
	tx.PreviousTag = strings.TrimSpace(previousTag)
	stagedCopy := staged
	stagedCopy.Files = append([]string(nil), staged.Files...)
	tx.Staged = &stagedCopy
	state, stateErr := LoadState()
	if stateErr != nil {
		removePrivateTransactionDir(txDir)
		return stateErr
	}
	prevDir, prevDirErr := PrevDir()
	if prevDirErr != nil {
		removePrivateTransactionDir(txDir)
		return prevDirErr
	}
	if state.Prev != nil && (state.Prev.TargetDir != tx.TargetDir || strings.TrimSpace(state.Prev.Tag) == "") {
		state.Prev = nil
	}
	if state.Prev != nil {
		if _, validErr := validatePrevSnapshot(prevDir, targetDir, nil); validErr != nil {
			state.Prev = nil
		}
	}
	if state.Prev != nil {
		prior := *state.Prev
		tx.PriorPrev = &prior
	}
	journalWritten := false
	defer func() {
		if !journalWritten {
			removePrivateTransactionDir(txDir)
		}
	}()

	if err := writeTargetPending(tx); err != nil {
		if matches, readErr := targetPendingMatches(tx); readErr == nil && matches {
			journalWritten = true
			return settlePreJournalFailure(tx, err)
		}
		return err
	}
	journalWritten = true
	if err := transactionCut("apply:target-published"); err != nil {
		return err
	}
	if err := writeUpdateJournal(tx); err != nil {
		if persisted, _, readErr := readUpdateJournal(tx.TargetDir); readErr == nil && persisted.ID == tx.ID {
			journalWritten = true
			return settleUpdateFailure(tx, err, false)
		}
		cleanupErr := removeTargetPending(tx)
		if cleanupErr == nil {
			journalWritten = false
		}
		return errors.Join(err, cleanupErr)
	}
	if err := transactionCut("apply:journal-published"); err != nil {
		return err
	}
	// The old State.Prev refers to the snapshot that will be superseded. Clear
	// it before the first binary changes, so a crash can never advertise the
	// newly constructed snapshot under an old release label.
	if err := clearUpdatePrevState(); err != nil {
		return settleUpdateFailure(tx, err, false)
	}
	if err := transactionCut("apply:prepared"); err != nil {
		return err
	}
	if err := installTransactionFiles(tx, transactionDesiredDir, tx.Desired, func(name string) error {
		return transactionCut("apply:replaced:" + name)
	}); err != nil {
		if isCutpointError(err) {
			return err
		}
		return settleUpdateFailure(tx, err, false)
	}
	if err := markUpdateTransactionCommitted(tx); err != nil {
		return settleUpdateFailure(tx, err, false)
	}
	if err := transactionCut("apply:committed"); err != nil {
		return err
	}
	if err := finishCommittedApply(tx, true); err != nil {
		if isCutpointError(err) {
			return err
		}
		return settleUpdateFailure(tx, err, true)
	}
	journalWritten = false
	return nil
}

func runRollbackTransaction(targetDir string) error {
	prev, err := PrevDir()
	if err != nil {
		return err
	}
	files, err := validatePrevSnapshot(prev, targetDir, nil)
	if errors.Is(err, os.ErrNotExist) {
		clearErr := clearInvalidPrevStateForTarget(targetDir)
		return errors.Join(fmt.Errorf("selfupdate: previous version is not available for rollback: %w", err), clearErr)
	}
	if err != nil {
		return errors.Join(err, clearInvalidPrevStateForTarget(targetDir))
	}
	names := transactionFileNames(files)
	tx, txDir, err := prepareUpdateTransaction("rollback", targetDir, prev, targetDir, names)
	if err != nil {
		return err
	}
	// The desired payload must be byte-for-byte identical to the complete,
	// validated snapshot. This also catches a concurrent or damaged snapshot
	// before any installed file is touched.
	if !sameTransactionFiles(tx.Desired, files) {
		removePrivateTransactionDir(txDir)
		return errors.New("selfupdate: rollback snapshot changed while it was being prepared")
	}
	journalWritten := false
	defer func() {
		if !journalWritten {
			removePrivateTransactionDir(txDir)
		}
	}()
	if err := writeTargetPending(tx); err != nil {
		if matches, readErr := targetPendingMatches(tx); readErr == nil && matches {
			journalWritten = true
			return settlePreJournalFailure(tx, err)
		}
		return err
	}
	journalWritten = true
	if err := transactionCut("rollback:target-published"); err != nil {
		return err
	}
	if err := writeUpdateJournal(tx); err != nil {
		if persisted, _, readErr := readUpdateJournal(tx.TargetDir); readErr == nil && persisted.ID == tx.ID {
			journalWritten = true
			return settleUpdateFailure(tx, err, false)
		}
		cleanupErr := removeTargetPending(tx)
		if cleanupErr == nil {
			journalWritten = false
		}
		return errors.Join(err, cleanupErr)
	}
	if err := transactionCut("rollback:prepared"); err != nil {
		return err
	}
	if err := installTransactionFiles(tx, transactionDesiredDir, tx.Desired, func(name string) error {
		return transactionCut("rollback:replaced:" + name)
	}); err != nil {
		if isCutpointError(err) {
			return err
		}
		return settleUpdateFailure(tx, err, false)
	}
	if err := markUpdateTransactionCommitted(tx); err != nil {
		return settleUpdateFailure(tx, err, false)
	}
	if err := transactionCut("rollback:committed"); err != nil {
		return err
	}
	if err := finishCommittedRollback(tx, true); err != nil {
		if isCutpointError(err) {
			return err
		}
		return settleUpdateFailure(tx, err, true)
	}
	journalWritten = false
	return nil
}

// isCutpointError distinguishes the deliberately unhandled test crash from a
// real I/O failure. A hook is only installed by package tests.
func isCutpointError(err error) bool {
	var cut *transactionCutpointError
	return errors.As(err, &cut)
}

func prepareUpdateTransaction(kind, targetDir, desiredSource, undoSource string, names []string) (updateTransaction, string, error) {
	canonical, err := CanonicalTargetDir(targetDir)
	if err != nil {
		return updateTransaction{}, "", err
	}
	updates, err := UpdatesDir()
	if err != nil {
		return updateTransaction{}, "", err
	}
	txDir, err := os.MkdirTemp(updates, updateTransactionBuild)
	if err != nil {
		return updateTransaction{}, "", err
	}
	if err := os.Chmod(txDir, fsmode.SecretDir); err != nil {
		removePrivateTransactionDir(txDir)
		return updateTransaction{}, "", err
	}
	tx := updateTransaction{
		Version:   updateTransactionVersion,
		ID:        updateTransactionPrefix + strings.TrimPrefix(filepath.Base(txDir), updateTransactionBuild),
		Kind:      kind,
		TargetDir: canonical,
	}
	fail := func(cause error) (updateTransaction, string, error) {
		removePrivateTransactionDir(txDir)
		return updateTransaction{}, "", cause
	}
	tx.Desired, err = snapshotNamedFiles(desiredSource, filepath.Join(txDir, transactionDesiredDir), names)
	if err != nil {
		return fail(fmt.Errorf("selfupdate: construct desired update snapshot: %w", err))
	}
	tx.Undo, err = snapshotNamedFiles(undoSource, filepath.Join(txDir, transactionUndoDir), names)
	if err != nil {
		return fail(fmt.Errorf("selfupdate: construct recovery snapshot: %w", err))
	}
	if err := writePrevSnapshotMetadata(filepath.Join(txDir, transactionUndoDir), canonical, tx.Undo); err != nil {
		return fail(fmt.Errorf("selfupdate: complete recovery snapshot: %w", err))
	}
	if err := validatePayloadDirectory(filepath.Join(txDir, transactionDesiredDir), tx.Desired, false); err != nil {
		return fail(err)
	}
	if _, err := validatePrevSnapshot(filepath.Join(txDir, transactionUndoDir), canonical, tx.Undo); err != nil {
		return fail(err)
	}
	if err := syncDirectory(txDir); err != nil {
		return fail(err)
	}
	finalDir := filepath.Join(updates, tx.ID)
	if err := durableRename(txDir, finalDir, false); err != nil {
		return fail(err)
	}
	txDir = finalDir
	return tx, txDir, nil
}

func snapshotNamedFiles(sourceDir, destinationDir string, names []string) ([]updateTxnFile, error) {
	if len(names) == 0 {
		return nil, errors.New("selfupdate: an empty binary set cannot be snapshotted")
	}
	if err := os.Mkdir(destinationDir, fsmode.SecretDir); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(names))
	files := make([]updateTxnFile, 0, len(names))
	for _, name := range names {
		if err := validateTransactionName(name); err != nil {
			return nil, err
		}
		key := canonicalTransactionName(name)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("selfupdate: duplicate binary %q", name)
		}
		seen[key] = struct{}{}
		source := filepath.Join(sourceDir, name)
		destination := filepath.Join(destinationDir, name)
		if updateSnapshotBeforeCopy != nil {
			if err := updateSnapshotBeforeCopy(source, destination); err != nil {
				return nil, err
			}
		}
		info, input, err := openRegularFile(source)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		mode := info.Mode().Perm()
		copyErr := writeFile(input, destination, mode)
		closeErr := input.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		record, err := inspectRegularFile(filepath.Join(destinationDir, name))
		if err != nil {
			return nil, err
		}
		record.Name = name
		files = append(files, record)
	}
	if err := syncDirectory(destinationDir); err != nil {
		return nil, err
	}
	return files, nil
}

func openRegularFile(path string) (os.FileInfo, *os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, nil, errors.New("not a regular file")
	}
	f, err := os.Open(path) //nolint:gosec // path is constructed from a validated base name
	if err != nil {
		return nil, nil, err
	}
	after, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		_ = f.Close()
		return nil, nil, errors.New("file identity changed while opening it")
	}
	return after, f, nil
}

func inspectRegularFile(path string) (updateTxnFile, error) {
	info, f, err := openRegularFile(path)
	if err != nil {
		return updateTxnFile{}, err
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, f)
	closeErr := f.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return updateTxnFile{}, err
	}
	return updateTxnFile{
		Size:   info.Size(),
		SHA256: hex.EncodeToString(h.Sum(nil)),
		Mode:   uint32(info.Mode().Perm()),
	}, nil
}

func writePrevSnapshotMetadata(dir, targetDir string, files []updateTxnFile) error {
	if err := writePrevTarget(dir, targetDir); err != nil {
		return err
	}
	manifest := prevSnapshotManifest{
		Version:   updateTransactionVersion,
		TargetDir: targetDir,
		Files:     append([]updateTxnFile(nil), files...),
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := writeFile(bytes.NewReader(data), filepath.Join(dir, prevSnapshotFileName), fsmode.SecretFile); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func validatePrevSnapshot(dir, targetDir string, expected []updateTxnFile) ([]updateTxnFile, error) {
	if err := validatePlainDirectory(dir); err != nil {
		return nil, err
	}
	if err := validatePrevTarget(dir, targetDir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, prevSnapshotFileName)
	var manifest prevSnapshotManifest
	if err := readStrictJSONFile(path, &manifest); err != nil {
		return nil, fmt.Errorf("selfupdate: rollback snapshot manifest is unavailable or damaged: %w", err)
	}
	canonical, err := CanonicalTargetDir(targetDir)
	if err != nil {
		return nil, err
	}
	if manifest.Version != updateTransactionVersion || manifest.TargetDir != canonical {
		return nil, errors.New("selfupdate: rollback snapshot manifest does not match this installation")
	}
	if err := validateTransactionFiles(manifest.Files); err != nil {
		return nil, fmt.Errorf("selfupdate: invalid rollback snapshot: %w", err)
	}
	if expected != nil && !sameTransactionFiles(manifest.Files, expected) {
		return nil, errors.New("selfupdate: rollback snapshot does not match the durable transaction")
	}
	if err := validatePayloadDirectory(dir, manifest.Files, true); err != nil {
		return nil, fmt.Errorf("selfupdate: incomplete rollback snapshot: %w", err)
	}
	return append([]updateTxnFile(nil), manifest.Files...), nil
}

func validatePayloadDirectory(dir string, files []updateTxnFile, snapshotMetadata bool) error {
	if err := validatePlainDirectory(dir); err != nil {
		return err
	}
	if err := validateTransactionFiles(files); err != nil {
		return err
	}
	allowed := make(map[string]updateTxnFile, len(files))
	for _, expected := range files {
		allowed[expected.Name] = expected
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(files))
	for _, entry := range entries {
		name := entry.Name()
		if snapshotMetadata && (name == prevTargetFileName || name == prevSnapshotFileName) {
			continue
		}
		expected, ok := allowed[name]
		if !ok {
			return fmt.Errorf("unexpected file %q", name)
		}
		actual, err := inspectRegularFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		actual.Name = name
		if !sameTransactionFile(actual, expected) {
			return fmt.Errorf("file %q failed size, mode, or SHA-256 validation", name)
		}
		seen[name] = struct{}{}
	}
	if len(seen) != len(files) {
		return errors.New("one or more declared files are missing")
	}
	return nil
}

func validatePlainDirectory(dir string) error {
	info, err := os.Lstat(dir) //nolint:gosec // G703: dir is a canonical transaction/install path and is rejected unless it is a plain directory
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("selfupdate: %s is not a plain directory", dir)
	}
	return nil
}

func validateTransactionFiles(files []updateTxnFile) error {
	if len(files) == 0 {
		return errors.New("binary list is empty")
	}
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if err := validateTransactionName(file.Name); err != nil {
			return err
		}
		key := canonicalTransactionName(file.Name)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate binary %q", file.Name)
		}
		seen[key] = struct{}{}
		decoded, err := hex.DecodeString(file.SHA256)
		if err != nil || len(decoded) != sha256.Size || file.Size < 0 || file.Mode&^0o777 != 0 {
			return fmt.Errorf("invalid metadata for binary %q", file.Name)
		}
	}
	return nil
}

func validateTransactionName(name string) error {
	reserved := map[string]struct{}{
		canonicalTransactionName(prevTargetFileName):                {},
		canonicalTransactionName(prevSnapshotFileName):              {},
		canonicalTransactionName(targetOperationLockFileName):       {},
		canonicalTransactionName(targetOperationIntentLockFileName): {},
		canonicalTransactionName(targetPendingFileName):             {},
	}
	key := canonicalTransactionName(name)
	if name == "" || filepath.Base(name) != name || name == "." || strings.Contains(name, ":") || strings.TrimRight(name, " .") != name {
		return fmt.Errorf("selfupdate: invalid binary name %q", name)
	}
	if _, protocolName := reserved[key]; protocolName {
		return fmt.Errorf("selfupdate: invalid binary name %q", name)
	}
	return nil
}

func canonicalTransactionName(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(name)
	}
	return name
}

func writeUpdateJournal(tx updateTransaction) error {
	updates, err := UpdatesDir()
	if err != nil {
		return err
	}
	// Validate the fully populated record before it becomes the durable
	// recovery authority. In particular, callers must not be able to publish a
	// journal that normal recovery would reject after the first binary changed.
	if err := validateUpdateTransaction(tx, updates, tx.TargetDir); err != nil {
		return err
	}
	journalPath := filepath.Join(updates, updateJournalFileName)
	if _, err := os.Lstat(journalPath); err == nil {
		return errors.New("selfupdate: another update transaction is pending")
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := json.Marshal(tx)
	if err != nil {
		return err
	}
	return writeFile(bytes.NewReader(data), journalPath, fsmode.SecretFile)
}

func targetPendingPath(targetDir string) string {
	return filepath.Join(targetDir, targetPendingFileName)
}

func pendingTransactionFor(tx updateTransaction) (targetPendingTransaction, error) {
	updates, err := UpdatesDir()
	if err != nil {
		return targetPendingTransaction{}, err
	}
	updates, err = filepath.Abs(updates)
	if err != nil {
		return targetPendingTransaction{}, err
	}
	return targetPendingTransaction{
		Version:   updateTransactionVersion,
		ID:        tx.ID,
		TargetDir: tx.TargetDir,
		Updates:   filepath.Clean(updates),
	}, nil
}

func writeTargetPending(tx updateTransaction) error {
	pending, err := pendingTransactionFor(tx)
	if err != nil {
		return err
	}
	path := targetPendingPath(tx.TargetDir)
	if _, err := os.Lstat(path); err == nil {
		return errors.New("selfupdate: another target update transaction is pending")
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	mode, err := targetCoordinationPermissions(tx.TargetDir)
	if err != nil {
		return err
	}
	return writeFile(bytes.NewReader(data), path, mode)
}

func readTargetPending(targetDir string) (targetPendingTransaction, error) {
	canonical, err := CanonicalTargetDir(targetDir)
	if err != nil {
		return targetPendingTransaction{}, err
	}
	var pending targetPendingTransaction
	if err := readStrictJSONFile(targetPendingPath(canonical), &pending); err != nil {
		return targetPendingTransaction{}, err
	}
	if pending.Version != updateTransactionVersion || pending.TargetDir != canonical ||
		filepath.Base(pending.ID) != pending.ID || !strings.HasPrefix(pending.ID, updateTransactionPrefix) {
		return targetPendingTransaction{}, errors.New("selfupdate: target pending marker is invalid")
	}
	absUpdates, err := filepath.Abs(pending.Updates)
	if err != nil || filepath.Clean(absUpdates) != pending.Updates {
		return targetPendingTransaction{}, errors.New("selfupdate: target pending marker has an invalid updates directory")
	}
	return pending, nil
}

func targetPendingMatches(tx updateTransaction) (bool, error) {
	pending, err := readTargetPending(tx.TargetDir)
	if err != nil {
		return false, err
	}
	want, err := pendingTransactionFor(tx)
	if err != nil {
		return false, err
	}
	return pending == want, nil
}

func removeTargetPending(tx updateTransaction) error {
	matches, err := targetPendingMatches(tx)
	if err != nil {
		return err
	}
	if !matches {
		return errors.New("selfupdate: target pending marker belongs to another transaction")
	}
	retired := filepath.Join(filepath.Dir(targetPendingPath(tx.TargetDir)), "."+tx.ID+".pending.completed")
	if err := durableRename(targetPendingPath(tx.TargetDir), retired, true); err != nil {
		return err
	}
	if err := os.Remove(retired); err != nil && !os.IsNotExist(err) {
		oblog.Component("selfupdate").Warn("retired target update marker was not removed", "file", retired, "err", err)
	}
	return nil
}

func readUpdateJournal(expectedTarget string) (updateTransaction, string, error) {
	updates, err := UpdatesDir()
	if err != nil {
		return updateTransaction{}, "", err
	}
	journalPath := filepath.Join(updates, updateJournalFileName)
	var tx updateTransaction
	if err := readStrictJSONFile(journalPath, &tx); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return updateTransaction{}, "", errNoUpdateTransaction
		}
		return updateTransaction{}, "", fmt.Errorf("selfupdate: update transaction journal is damaged: %w", err)
	}
	if err := validateUpdateTransaction(tx, updates, expectedTarget); err != nil {
		return updateTransaction{}, "", err
	}
	return tx, filepath.Join(updates, tx.ID), nil
}

func validateUpdateTransaction(tx updateTransaction, updates, expectedTarget string) error {
	if tx.Version != updateTransactionVersion || (tx.Kind != "apply" && tx.Kind != "rollback") {
		return errors.New("selfupdate: unsupported update transaction journal")
	}
	if filepath.Base(tx.ID) != tx.ID || !strings.HasPrefix(tx.ID, updateTransactionPrefix) {
		return errors.New("selfupdate: invalid update transaction directory")
	}
	txDir := filepath.Join(updates, tx.ID)
	if filepath.Clean(filepath.Dir(txDir)) != filepath.Clean(updates) {
		return errors.New("selfupdate: update transaction escapes the updates directory")
	}
	if err := validatePlainDirectory(txDir); err != nil {
		return fmt.Errorf("selfupdate: update transaction payload is unavailable: %w", err)
	}
	want, err := CanonicalTargetDir(expectedTarget)
	if err != nil {
		return err
	}
	stored, err := CanonicalTargetDir(tx.TargetDir)
	if err != nil || stored != tx.TargetDir || stored != want {
		return fmt.Errorf("selfupdate: pending transaction belongs to %q, not %q", tx.TargetDir, want)
	}
	if err := validatePlainDirectory(tx.TargetDir); err != nil {
		return fmt.Errorf("selfupdate: update target is unsafe: %w", err)
	}
	if err := validateTransactionFiles(tx.Desired); err != nil {
		return err
	}
	if err := validateTransactionFiles(tx.Undo); err != nil {
		return err
	}
	if !sameTransactionNames(tx.Desired, tx.Undo) {
		return errors.New("selfupdate: transaction desired and recovery sets differ")
	}
	switch tx.Kind {
	case "apply":
		if tx.Staged == nil || !tx.Staged.Verified || strings.TrimSpace(tx.Staged.Tag) == "" || len(tx.Staged.Files) == 0 {
			return errors.New("selfupdate: apply transaction has invalid staged-release metadata")
		}
		seen := make(map[string]struct{}, len(tx.Staged.Files))
		for _, name := range tx.Staged.Files {
			if err := validateTransactionName(name); err != nil {
				return err
			}
			key := canonicalTransactionName(name)
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("selfupdate: duplicate staged binary %q", name)
			}
			seen[key] = struct{}{}
		}
		if tx.PriorPrev != nil && (tx.PriorPrev.TargetDir != tx.TargetDir || strings.TrimSpace(tx.PriorPrev.Tag) == "") {
			return errors.New("selfupdate: invalid prior rollback metadata in transaction")
		}
	case "rollback":
		if tx.Staged != nil || tx.PriorPrev != nil || tx.PreviousTag != "" {
			return errors.New("selfupdate: rollback transaction contains apply-only metadata")
		}
	}
	return nil
}

func readStrictJSONFile(path string, dst any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxTransactionJSON {
		return errors.New("not a small regular JSON file")
	}
	data, err := os.ReadFile(path) //nolint:gosec // validated private transaction path
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func markUpdateTransactionCommitted(tx updateTransaction) error {
	updates, err := UpdatesDir()
	if err != nil {
		return err
	}
	path := filepath.Join(updates, updateCommitFileName)
	if _, err := os.Lstat(path); err == nil {
		return errors.New("selfupdate: stale transaction commit marker exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	return writeFile(strings.NewReader(tx.ID), path, fsmode.SecretFile)
}

func updateTransactionCommitted(tx updateTransaction) (bool, error) {
	updates, err := UpdatesDir()
	if err != nil {
		return false, err
	}
	path := filepath.Join(updates, updateCommitFileName)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() > 256 {
		return false, errors.New("selfupdate: transaction commit marker is damaged")
	}
	data, err := os.ReadFile(path) //nolint:gosec // fixed private marker path
	if err != nil {
		return false, err
	}
	if string(data) != tx.ID {
		return false, errors.New("selfupdate: transaction commit marker does not match the journal")
	}
	return true, nil
}

func recoverUpdateTransactionLocked(expectedTarget string) error {
	pending, pendingErr := readTargetPending(expectedTarget)
	if os.IsNotExist(pendingErr) {
		// No target-scoped authority exists. A per-profile journal here is
		// untrusted/stale and must not authorize writes to a shared install.
		if err := validateNoTargetAuthority(expectedTarget); err != nil {
			return err
		}
		return cleanupOrphanTransactionState(expectedTarget)
	}
	if pendingErr != nil {
		return pendingErr
	}
	updates, err := UpdatesDir()
	if err != nil {
		return err
	}
	absUpdates, err := filepath.Abs(updates)
	if err != nil {
		return err
	}
	if filepath.Clean(absUpdates) != pending.Updates {
		return fmt.Errorf("selfupdate: installation has an unresolved update owned by another profile at %s", pending.Updates)
	}
	tx, _, err := readUpdateJournal(expectedTarget)
	if errors.Is(err, errNoUpdateTransaction) {
		txDir := filepath.Join(pending.Updates, pending.ID)
		if _, completedErr := os.Lstat(filepath.Join(txDir, transactionCompletedFile)); os.IsNotExist(completedErr) {
			return recoverPreJournalTransaction(pending)
		} else if completedErr != nil {
			return completedErr
		}
		return recoverCompletedPendingTransaction(pending)
	}
	if err != nil {
		return err
	}
	if tx.ID != pending.ID {
		return errors.New("selfupdate: target pending marker does not match the profile journal")
	}
	committed, err := updateTransactionCommitted(tx)
	if err != nil {
		return err
	}
	if committed {
		switch tx.Kind {
		case "apply":
			return finishCommittedApply(tx, false)
		case "rollback":
			return finishCommittedRollback(tx, false)
		}
	}
	return rollbackPreparedTransaction(tx)
}

// validateNoTargetAuthority proves that this profile has no active journal
// for expectedTarget. A private journal alone must never authorize target
// writes: the installation-scoped marker is published first and is the durable
// proof that the transaction owns this exact installation.
func validateNoTargetAuthority(expectedTarget string) error {
	if _, _, err := readUpdateJournal(expectedTarget); err == nil {
		return errors.New("selfupdate: profile transaction has no target ownership marker")
	} else if !errors.Is(err, errNoUpdateTransaction) {
		return err
	}
	return nil
}

func recoverPreJournalTransaction(pending targetPendingTransaction) error {
	txDir := filepath.Join(pending.Updates, pending.ID)
	if err := validatePlainDirectory(txDir); err != nil {
		return fmt.Errorf("selfupdate: pre-journal recovery payload is unavailable: %w", err)
	}
	// No binary mutation can happen before the profile journal is durably
	// published. Retire the exact owner marker, then delete only its direct,
	// validated private transaction directory.
	tx := updateTransaction{ID: pending.ID, TargetDir: pending.TargetDir}
	if err := removeTargetPending(tx); err != nil {
		return err
	}
	removePrivateTransactionDir(txDir)
	return nil
}

func settlePreJournalFailure(tx updateTransaction, original error) error {
	pending, err := pendingTransactionFor(tx)
	if err != nil {
		return errors.Join(original, &RecoveryPendingError{err: err})
	}
	if recoveryErr := recoverPreJournalTransaction(pending); recoveryErr != nil {
		return errors.Join(original, &RecoveryPendingError{err: recoveryErr})
	}
	return original
}

func recoverCompletedPendingTransaction(pending targetPendingTransaction) error {
	txDir := filepath.Join(pending.Updates, pending.ID)
	tx, err := readCompletedUpdateTransaction(txDir, pending.TargetDir)
	if err != nil {
		return fmt.Errorf("selfupdate: pending completed transaction locator is unavailable: %w", err)
	}
	// The journal was retired only after the selected installed generation and
	// State/Prev relationship had been fully validated. Re-prove those terminal
	// invariants before retiring the target owner marker.
	switch tx.Kind {
	case "apply":
		if err := validateInstalledSet(tx.TargetDir, tx.Desired); err != nil {
			return err
		}
		prev, err := PrevDir()
		if err != nil {
			return err
		}
		if _, err := validatePrevSnapshot(prev, tx.TargetDir, tx.Undo); err != nil {
			return err
		}
		state, err := LoadState()
		if err != nil {
			return err
		}
		if tx.PreviousTag != "" && (state.Prev == nil || state.Prev.Tag != tx.PreviousTag || state.Prev.TargetDir != tx.TargetDir) {
			return errors.New("selfupdate: completed apply state is not reconciled")
		}
	case "rollback":
		if err := validateInstalledSet(tx.TargetDir, tx.Desired); err != nil {
			return err
		}
		state, err := LoadState()
		if err != nil {
			return err
		}
		if state.Prev != nil {
			return errors.New("selfupdate: completed rollback remains advertised")
		}
	default:
		return errors.New("selfupdate: completed transaction has an invalid kind")
	}
	if err := removeTargetPending(tx); err != nil {
		return err
	}
	cleanupTransactionArtifacts(tx)
	return nil
}

func rollbackPreparedTransaction(tx updateTransaction) error {
	txDir, err := transactionDirectory(tx)
	if err != nil {
		return err
	}
	undoDir := filepath.Join(txDir, transactionUndoDir)
	if _, err := validatePrevSnapshot(undoDir, tx.TargetDir, tx.Undo); err != nil {
		return fmt.Errorf("selfupdate: recovery snapshot is incomplete; installation was not modified further: %w", err)
	}
	if err := installTransactionFiles(tx, transactionUndoDir, tx.Undo, nil); err != nil {
		return fmt.Errorf("selfupdate: restore complete pre-update binary set: %w", err)
	}
	if err := validateInstalledSet(tx.TargetDir, tx.Undo); err != nil {
		return err
	}
	if tx.Kind == "apply" {
		if err := restorePriorPrevState(tx); err != nil {
			return fmt.Errorf("selfupdate: restore pre-update state: %w", err)
		}
	}
	if err := completeUpdateTransaction(tx); err != nil {
		return err
	}
	if err := removeTargetPending(tx); err != nil {
		return err
	}
	cleanupTransactionArtifacts(tx)
	return nil
}

func finishCommittedApply(tx updateTransaction, cuts bool) error {
	txDir, err := transactionDirectory(tx)
	if err != nil {
		return err
	}
	if err := validatePayloadDirectory(filepath.Join(txDir, transactionDesiredDir), tx.Desired, false); err != nil {
		return fmt.Errorf("selfupdate: committed update payload is incomplete: %w", err)
	}
	if err := installTransactionFiles(tx, transactionDesiredDir, tx.Desired, nil); err != nil {
		return err
	}
	if err := validateInstalledSet(tx.TargetDir, tx.Desired); err != nil {
		return err
	}
	if err := publishApplySnapshot(tx, cuts); err != nil {
		return err
	}
	if cuts {
		if err := transactionCut("apply:prev-published"); err != nil {
			return err
		}
	}
	if err := reconcileCommittedApplyState(tx); err != nil {
		return fmt.Errorf("selfupdate: publish committed update state: %w", err)
	}
	if err := completeUpdateTransaction(tx); err != nil {
		return err
	}
	if cuts {
		if err := transactionCut("apply:journal-retired"); err != nil {
			return err
		}
	}
	if err := removeTargetPending(tx); err != nil {
		return err
	}
	if tx.Staged != nil {
		removeManagedStage(tx.Staged.Dir)
	}
	cleanupTransactionArtifacts(tx)
	return nil
}

func finishCommittedRollback(tx updateTransaction, cuts bool) error {
	txDir, err := transactionDirectory(tx)
	if err != nil {
		return err
	}
	if err := validatePayloadDirectory(filepath.Join(txDir, transactionDesiredDir), tx.Desired, false); err != nil {
		return fmt.Errorf("selfupdate: committed rollback payload is incomplete: %w", err)
	}
	if err := installTransactionFiles(tx, transactionDesiredDir, tx.Desired, nil); err != nil {
		return err
	}
	if err := validateInstalledSet(tx.TargetDir, tx.Desired); err != nil {
		return err
	}
	// Stop advertising the snapshot before moving it out of updates/prev. A
	// crash may leave the journal pending, but State.Prev must never point at a
	// missing or only partly recoverable directory.
	if err := clearUpdatePrevState(); err != nil {
		return fmt.Errorf("selfupdate: clear consumed rollback state: %w", err)
	}
	if err := consumeRollbackSnapshot(tx); err != nil {
		return err
	}
	if cuts {
		if err := transactionCut("rollback:snapshot-consumed"); err != nil {
			return err
		}
	}
	if err := completeUpdateTransaction(tx); err != nil {
		return err
	}
	if cuts {
		if err := transactionCut("rollback:journal-retired"); err != nil {
			return err
		}
	}
	if err := removeTargetPending(tx); err != nil {
		return err
	}
	cleanupTransactionArtifacts(tx)
	return nil
}

func publishApplySnapshot(tx updateTransaction, cuts bool) error {
	txDir, err := transactionDirectory(tx)
	if err != nil {
		return err
	}
	updates, err := UpdatesDir()
	if err != nil {
		return err
	}
	prev := filepath.Join(updates, "prev")
	undo := filepath.Join(txDir, transactionUndoDir)
	if _, err := validatePrevSnapshot(prev, tx.TargetDir, tx.Undo); err == nil {
		return nil
	}
	if _, err := validatePrevSnapshot(undo, tx.TargetDir, tx.Undo); err != nil {
		return fmt.Errorf("selfupdate: complete rollback snapshot cannot be published: %w", err)
	}
	obsolete := filepath.Join(txDir, transactionObsoletePrev)
	if info, err := os.Lstat(prev); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("selfupdate: existing rollback path is not a plain directory")
		}
		if _, err := os.Lstat(obsolete); err == nil {
			if err := removePlainPrivateDir(prev); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		} else if err := durableRename(prev, obsolete, false); err != nil {
			return fmt.Errorf("selfupdate: preserve superseded rollback snapshot: %w", err)
		}
		if err := syncDirectory(updates); err != nil {
			return err
		}
		if cuts {
			if err := transactionCut("apply:old-prev-moved"); err != nil {
				return err
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := durableRename(undo, prev, false); err != nil {
		return fmt.Errorf("selfupdate: publish complete rollback snapshot: %w", err)
	}
	if err := syncDirectory(updates); err != nil {
		return err
	}
	_, err = validatePrevSnapshot(prev, tx.TargetDir, tx.Undo)
	return err
}

func consumeRollbackSnapshot(tx updateTransaction) error {
	txDir, err := transactionDirectory(tx)
	if err != nil {
		return err
	}
	prev, err := PrevDir()
	if err != nil {
		return err
	}
	consumed := filepath.Join(txDir, transactionConsumedPrev)
	if _, err := os.Lstat(consumed); err == nil {
		if _, err := validatePrevSnapshot(consumed, tx.TargetDir, tx.Desired); err != nil {
			return fmt.Errorf("selfupdate: consumed rollback snapshot is incomplete: %w", err)
		}
		// A durable directory rename normally leaves only consumed. If a crash
		// exposed both names, remove the duplicate only after proving that both
		// are the exact complete snapshot selected by the transaction.
		if _, err := os.Lstat(prev); os.IsNotExist(err) {
			return nil
		} else if err != nil {
			return err
		}
		if _, err := validatePrevSnapshot(prev, tx.TargetDir, tx.Desired); err != nil {
			return fmt.Errorf("selfupdate: rollback snapshot has an ambiguous post-rename state: %w", err)
		}
		if err := removePlainPrivateDir(prev); err != nil {
			return err
		}
		updates, err := UpdatesDir()
		if err != nil {
			return err
		}
		return syncDirectory(updates)
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := validatePrevSnapshot(prev, tx.TargetDir, tx.Desired); err != nil {
		return fmt.Errorf("selfupdate: rollback snapshot cannot be consumed safely: %w", err)
	}
	if err := durableRename(prev, consumed, false); err != nil {
		return err
	}
	updates, err := UpdatesDir()
	if err != nil {
		return err
	}
	return syncDirectory(updates)
}

func installTransactionFiles(tx updateTransaction, payloadName string, files []updateTxnFile, after func(string) error) error {
	txDir, err := transactionDirectory(tx)
	if err != nil {
		return err
	}
	if err := validatePlainDirectory(tx.TargetDir); err != nil {
		return fmt.Errorf("selfupdate: update target is unsafe: %w", err)
	}
	payloadDir := filepath.Join(txDir, payloadName)
	for _, file := range files {
		target := filepath.Join(tx.TargetDir, file.Name)
		matches, err := fileMatchesRecord(target, file)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if !matches {
			if err := installPayloadFile(filepath.Join(payloadDir, file.Name), target, file, tx.ID); err != nil {
				return fmt.Errorf("selfupdate: install %s: %w", file.Name, err)
			}
		}
		if after != nil {
			if err := after(file.Name); err != nil {
				return err
			}
		}
	}
	return validateInstalledSet(tx.TargetDir, files)
}

func displacedTransactionPath(target string, expected updateTxnFile, txID string) string {
	digest := strings.ToLower(expected.SHA256)
	if len(digest) > 16 {
		digest = digest[:16]
	}
	return target + ".update-" + strings.TrimPrefix(txID, updateTransactionPrefix) + "-" + digest
}

func installPayloadFile(source, target string, expected updateTxnFile, txID string) error {
	if matches, err := fileMatchesRecord(source, expected); err != nil || !matches {
		if err == nil {
			err = errors.New("payload digest mismatch")
		}
		return err
	}
	if info, err := os.Lstat(target); err == nil && !info.Mode().IsRegular() {
		return errors.New("target is not a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	in, err := os.Open(source) //nolint:gosec // validated transaction payload
	if err != nil {
		return err
	}
	defer oblog.CloseQuiet("selfupdate", "transaction payload", in)
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(target)+".update-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	installed := false
	defer func() {
		_ = tmp.Close()
		if !installed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(os.FileMode(expected.Mode)); err != nil {
		return err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := durableRename(tmpPath, target, true); err == nil {
		installed = true
		return nil
	} else {
		// MoveFileEx can fail because the destination image is still mapped by
		// the running process. No remove-then-rename window is allowed: first
		// durably move that image aside, then durably publish the replacement.
		if runtime.GOOS != "windows" {
			return err
		}
		if matches, matchErr := fileMatchesRecord(target, expected); matchErr == nil && matches {
			installed = true
			return nil
		}
	}
	displaced := displacedTransactionPath(target, expected, txID)
	if info, statErr := os.Lstat(displaced); statErr == nil {
		if !info.Mode().IsRegular() {
			return errors.New("existing displaced path is not a regular file")
		}
		return errors.New("stale displaced transaction file exists")
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if renameErr := durableRename(target, displaced, false); renameErr != nil {
		return renameErr
	}
	if err := transactionCut("install:displaced:" + filepath.Base(target)); err != nil {
		return err
	}
	if renameErr := durableRename(tmpPath, target, false); renameErr != nil {
		// This restoration is best-effort only. If it fails, the durable journal
		// and digest-bound displaced locator remain sufficient for recovery.
		_ = durableRename(displaced, target, false)
		return renameErr
	}
	installed = true
	return nil
}

func validateInstalledSet(targetDir string, files []updateTxnFile) error {
	for _, expected := range files {
		matches, err := fileMatchesRecord(filepath.Join(targetDir, expected.Name), expected)
		if err != nil {
			return fmt.Errorf("selfupdate: validate installed %s: %w", expected.Name, err)
		}
		if !matches {
			return fmt.Errorf("selfupdate: installed binary %s does not match the transaction", expected.Name)
		}
	}
	return nil
}

func fileMatchesRecord(path string, expected updateTxnFile) (bool, error) {
	actual, err := inspectRegularFile(path)
	if err != nil {
		return false, err
	}
	actual.Name = expected.Name
	return sameTransactionFile(actual, expected), nil
}

func sameTransactionFile(a, b updateTxnFile) bool {
	return a.Name == b.Name && a.Size == b.Size && strings.EqualFold(a.SHA256, b.SHA256) && a.Mode == b.Mode
}

func sameTransactionFiles(a, b []updateTxnFile) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameTransactionFile(a[i], b[i]) {
			return false
		}
	}
	return true
}

func sameTransactionNames(a, b []updateTxnFile) bool {
	if len(a) != len(b) {
		return false
	}
	aNames := transactionFileNames(a)
	bNames := transactionFileNames(b)
	for i := range aNames {
		aNames[i] = canonicalTransactionName(aNames[i])
	}
	for i := range bNames {
		bNames[i] = canonicalTransactionName(bNames[i])
	}
	sort.Strings(aNames)
	sort.Strings(bNames)
	for i := range aNames {
		if aNames[i] != bNames[i] {
			return false
		}
	}
	return true
}

func transactionFileNames(files []updateTxnFile) []string {
	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, file.Name)
	}
	return names
}

func transactionDirectory(tx updateTransaction) (string, error) {
	updates, err := UpdatesDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(updates, tx.ID)
	if filepath.Base(tx.ID) != tx.ID || !strings.HasPrefix(tx.ID, updateTransactionPrefix) || filepath.Clean(filepath.Dir(dir)) != filepath.Clean(updates) {
		return "", errors.New("selfupdate: invalid transaction directory")
	}
	if err := validatePlainDirectory(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func completeUpdateTransaction(tx updateTransaction) error {
	updates, err := UpdatesDir()
	if err != nil {
		return err
	}
	txDir, err := transactionDirectory(tx)
	if err != nil {
		return err
	}
	journal := filepath.Join(updates, updateJournalFileName)
	completed := filepath.Join(txDir, transactionCompletedFile)
	if _, err := os.Lstat(journal); err == nil {
		// Retiring the journal is the durable completion point. The completed
		// copy remains as a permanent, digest-bound cleanup locator for any
		// running Windows image that could not yet be deleted.
		if err := durableRename(journal, completed, true); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	} else if _, completedErr := readCompletedUpdateTransaction(txDir, tx.TargetDir); completedErr != nil {
		return errors.Join(err, completedErr)
	}
	commit := filepath.Join(updates, updateCommitFileName)
	if _, err := os.Lstat(commit); err == nil {
		if err := durableRename(commit, filepath.Join(txDir, transactionRetiredCommit), true); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func readCompletedUpdateTransaction(txDir, expectedTarget string) (updateTransaction, error) {
	updates := filepath.Dir(txDir)
	var tx updateTransaction
	if err := readStrictJSONFile(filepath.Join(txDir, transactionCompletedFile), &tx); err != nil {
		return updateTransaction{}, err
	}
	if filepath.Clean(filepath.Join(updates, tx.ID)) != filepath.Clean(txDir) {
		return updateTransaction{}, errors.New("selfupdate: completed transaction locator has the wrong directory")
	}
	if err := validateCompletedUpdateTransaction(tx, updates, expectedTarget); err != nil {
		return updateTransaction{}, err
	}
	return tx, nil
}

func validateCompletedUpdateTransaction(tx updateTransaction, updates, expectedTarget string) error {
	if tx.Version != updateTransactionVersion || (tx.Kind != "apply" && tx.Kind != "rollback") {
		return errors.New("selfupdate: unsupported completed update transaction")
	}
	if filepath.Base(tx.ID) != tx.ID || !strings.HasPrefix(tx.ID, updateTransactionPrefix) ||
		filepath.Clean(filepath.Dir(filepath.Join(updates, tx.ID))) != filepath.Clean(updates) {
		return errors.New("selfupdate: invalid completed transaction directory")
	}
	want, err := CanonicalTargetDir(expectedTarget)
	if err != nil {
		return err
	}
	stored, err := CanonicalTargetDir(tx.TargetDir)
	if err != nil || stored != tx.TargetDir || stored != want {
		return errors.New("selfupdate: completed transaction belongs to another installation")
	}
	if err := validateTransactionFiles(tx.Desired); err != nil {
		return err
	}
	if err := validateTransactionFiles(tx.Undo); err != nil {
		return err
	}
	if !sameTransactionNames(tx.Desired, tx.Undo) {
		return errors.New("selfupdate: completed transaction generations differ")
	}
	if tx.Kind == "apply" {
		if tx.Staged == nil || !tx.Staged.Verified || strings.TrimSpace(tx.Staged.Tag) == "" {
			return errors.New("selfupdate: completed apply metadata is invalid")
		}
	} else if tx.Staged != nil || tx.PriorPrev != nil || tx.PreviousTag != "" {
		return errors.New("selfupdate: completed rollback contains apply-only metadata")
	}
	return nil
}

func cleanupOrphanTransactionState(expectedTarget string) error {
	updates, err := UpdatesDir()
	if err != nil {
		return err
	}
	path := filepath.Join(updates, updateCommitFileName)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Size() > 256 {
			return errors.New("selfupdate: orphan transaction commit marker is damaged")
		}
		// With no journal the marker has no authority. Rename it durably out of
		// the protocol namespace; deleting the ignored tombstone is optional.
		if err := durableRename(path, filepath.Join(updates, orphanCommitTombstone), true); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	entries, err := os.ReadDir(updates)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), updateTransactionPrefix) {
			continue
		}
		candidate := filepath.Join(updates, entry.Name())
		info, statErr := os.Lstat(candidate)
		if statErr != nil {
			return statErr
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("selfupdate: orphan transaction path %s is not a plain directory", candidate)
		}
		if _, completedErr := os.Lstat(filepath.Join(candidate, transactionCompletedFile)); completedErr == nil {
			var locator updateTransaction
			if err := readStrictJSONFile(filepath.Join(candidate, transactionCompletedFile), &locator); err != nil {
				return fmt.Errorf("selfupdate: completed cleanup locator is damaged: %w", err)
			}
			storedTarget, err := CanonicalTargetDir(locator.TargetDir)
			if err != nil || storedTarget != locator.TargetDir {
				return errors.New("selfupdate: completed cleanup locator has an invalid target")
			}
			if err := validateCompletedUpdateTransaction(locator, updates, storedTarget); err != nil {
				return fmt.Errorf("selfupdate: completed cleanup locator is damaged: %w", err)
			}
			expectedCanonical, err := CanonicalTargetDir(expectedTarget)
			if err != nil {
				return err
			}
			if storedTarget != expectedCanonical {
				// A completed locator has no update authority. Leave install A's
				// exact deferred cleanup for A instead of blocking install B.
				continue
			}
			tx, readErr := readCompletedUpdateTransaction(candidate, expectedTarget)
			if readErr != nil {
				return fmt.Errorf("selfupdate: completed cleanup locator is damaged: %w", readErr)
			}
			cleanupTransactionArtifacts(tx)
			continue
		} else if !os.IsNotExist(completedErr) {
			return completedErr
		}
		if err := os.RemoveAll(candidate); err != nil {
			return err
		}
	}
	return syncDirectory(updates)
}

func settleUpdateFailure(tx updateTransaction, original error, knownCommitted bool) error {
	committed := knownCommitted
	if diskCommitted, err := updateTransactionCommitted(tx); err == nil {
		committed = committed || diskCommitted
	}
	if recoveryErr := recoverUpdateTransactionLocked(tx.TargetDir); recoveryErr != nil {
		return errors.Join(original, &RecoveryPendingError{err: recoveryErr})
	}
	if committed {
		// The durable commit point selected the new/rolled-back full set. If
		// recovery completed that decision, the operation itself succeeded.
		return nil
	}
	return original
}

func clearUpdatePrevState() error {
	_, err := UpdateState(func(state *State) error {
		state.Prev = nil
		return nil
	})
	return err
}

func clearInvalidPrevStateForTarget(targetDir string) error {
	canonical, err := CanonicalTargetDir(targetDir)
	if err != nil {
		return err
	}
	_, err = UpdateState(func(state *State) error {
		if state.Prev != nil && state.Prev.TargetDir == canonical {
			state.Prev = nil
		}
		return nil
	})
	return err
}

// ValidatedRollbackInfo returns rollback metadata only when its complete,
// digest-verified snapshot belongs to targetDir. The stage lifecycle lock
// serializes this read with all supported snapshot writers without taking the
// target's exclusive binary lock (which would wait for running consumers).
// Invalid metadata for this target is cleared so status/UI never advertises a
// rollback that cannot be performed.
func ValidatedRollbackInfo(targetDir string) (_ *RelInfo, resultErr error) {
	stage, err := acquireStageOperationLock()
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, stage.Unlock()) }()

	canonical, err := CanonicalTargetDir(targetDir)
	if err != nil {
		return nil, err
	}
	state, err := LoadState()
	if err != nil || state.Prev == nil {
		return nil, err
	}
	advertised := *state.Prev
	if advertised.TargetDir != canonical {
		if advertised.TargetDir == "" {
			_, clearErr := UpdateState(func(current *State) error {
				if current.Prev != nil && *current.Prev == advertised {
					current.Prev = nil
				}
				return nil
			})
			return nil, clearErr
		}
		return nil, nil
	}
	prev, err := PrevDir()
	if err == nil {
		_, err = validatePrevSnapshot(prev, canonical, nil)
	}
	if err == nil {
		return &advertised, nil
	}
	_, clearErr := UpdateState(func(current *State) error {
		if current.Prev != nil && *current.Prev == advertised {
			current.Prev = nil
		}
		return nil
	})
	return nil, errors.Join(err, clearErr)
}

func restorePriorPrevState(tx updateTransaction) error {
	var prior *RelInfo
	if tx.PriorPrev != nil {
		prev, err := PrevDir()
		if err != nil {
			return err
		}
		if _, err := validatePrevSnapshot(prev, tx.TargetDir, nil); err == nil {
			copy := *tx.PriorPrev
			prior = &copy
		} else {
			// Recovery of the installed binaries can still finish safely, but an
			// incomplete old snapshot must never remain advertised in State.Prev.
			oblog.Component("selfupdate").Warn("prior rollback snapshot was not restored to state", "err", err)
		}
	}
	_, err := UpdateState(func(state *State) error {
		state.Prev = prior
		return nil
	})
	return err
}

func reconcileCommittedApplyState(tx updateTransaction) error {
	_, err := UpdateState(func(state *State) error {
		if tx.PreviousTag != "" {
			state.Prev = &RelInfo{Tag: tx.PreviousTag, TargetDir: tx.TargetDir}
		} else {
			// The legacy Apply API does not know the prior release tag. Never
			// invent metadata that could mislabel a valid snapshot.
			state.Prev = nil
		}
		if sameStagedTransaction(state.Staged, tx.Staged) {
			state.Staged = nil
		}
		return nil
	})
	return err
}

func sameStagedTransaction(a, b *StagedInfo) bool {
	if a == nil || b == nil || a.Tag != b.Tag || a.Dir != b.Dir || a.Verified != b.Verified || len(a.Files) != len(b.Files) {
		return false
	}
	for i := range a.Files {
		if a.Files[i] != b.Files[i] {
			return false
		}
	}
	return true
}

func cleanupTransactionArtifacts(tx updateTransaction) {
	if err := cleanupDisplacedTransactionFiles(tx); err != nil {
		oblog.Component("selfupdate").Warn("one or more displaced executables remain queued for exact cleanup", "transaction", tx.ID, "err", err)
	}
	txDir, err := transactionDirectory(tx)
	if err != nil {
		return
	}
	// Keep only the small completed journal. It is the durable, exact locator
	// used on every later startup; no broad <binary>.update-* scan is needed.
	for _, name := range []string{
		transactionDesiredDir,
		transactionUndoDir,
		transactionObsoletePrev,
		transactionConsumedPrev,
	} {
		path := filepath.Join(txDir, name)
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		}
		if err := removePlainPrivateDir(path); err != nil {
			oblog.Component("selfupdate").Warn("completed transaction payload was not removed", "path", path, "err", err)
		}
	}
	if err := os.Remove(filepath.Join(txDir, transactionRetiredCommit)); err != nil && !os.IsNotExist(err) {
		oblog.Component("selfupdate").Warn("retired transaction commit marker was not removed", "transaction", tx.ID, "err", err)
	}
	if cleanErr := cleanupDisplacedTransactionFiles(tx); cleanErr == nil {
		removePrivateTransactionDir(txDir)
	}
}

func cleanupDisplacedTransactionFiles(tx updateTransaction) error {
	byName := make(map[string][]updateTxnFile, len(tx.Desired))
	for _, set := range [][]updateTxnFile{tx.Desired, tx.Undo} {
		for _, record := range set {
			key := canonicalTransactionName(record.Name)
			byName[key] = append(byName[key], record)
		}
	}
	seen := make(map[string]struct{}, len(tx.Desired)+len(tx.Undo))
	var result error
	for _, installed := range append(append([]updateTxnFile(nil), tx.Desired...), tx.Undo...) {
		path := displacedTransactionPath(filepath.Join(tx.TargetDir, installed.Name), installed, tx.ID)
		if _, duplicate := seen[path]; duplicate {
			continue
		}
		seen[path] = struct{}{}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		if !info.Mode().IsRegular() {
			result = errors.Join(result, fmt.Errorf("%s is not a regular displaced file", path))
			continue
		}
		actual, err := inspectRegularFile(path)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		actual.Name = installed.Name
		valid := false
		for _, candidate := range byName[canonicalTransactionName(installed.Name)] {
			if sameTransactionFile(actual, candidate) {
				valid = true
				break
			}
		}
		if !valid {
			result = errors.Join(result, fmt.Errorf("%s does not match either transaction generation", path))
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func removePrivateTransactionDir(dir string) {
	updates, err := updatesDirPath()
	if err != nil {
		return
	}
	absUpdates, err := filepath.Abs(updates)
	if err != nil {
		return
	}
	absDir, err := filepath.Abs(dir)
	if err != nil || filepath.Clean(filepath.Dir(absDir)) != filepath.Clean(absUpdates) || !strings.HasPrefix(filepath.Base(absDir), updateTransactionPrefix) {
		return
	}
	if info, err := os.Lstat(absDir); err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		if err := os.RemoveAll(absDir); err != nil {
			oblog.Component("selfupdate").Warn("transaction directory was not removed", "dir", absDir, "err", err)
		}
	}
}

func removePlainPrivateDir(dir string) error {
	if err := validatePlainDirectory(dir); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

func syncDirectory(dir string) error {
	f, err := os.Open(dir) //nolint:gosec // caller supplies a validated internal directory
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	// Windows commonly rejects directory fsync. File replacement and journal
	// creation are still flushed individually there; Unix must persist the
	// directory entry to make the recovery protocol durable.
	if runtime.GOOS == "windows" {
		syncErr = nil
	}
	return errors.Join(syncErr, closeErr)
}
