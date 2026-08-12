package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/storage"
)

const (
	restoreIntentKey      = "onebase.internal.restore.intent.v1"
	restoreIntentVersion  = 1
	restoreResolveTimeout = 30 * time.Second
	// A valid universal archive may expand to 64 GiB. Six hours keeps cleanup
	// bounded without aborting a restore on a slow encrypted or network volume.
	restoreOperationWindow = 6 * time.Hour
)

var ErrRestoreRecoveryRequired = errors.New("restore recovery is required")

// restoreOperationMu prevents an in-process recovery pass from observing the
// durable pending marker while its owning restore transaction is still open.
// Cross-process server/CLI coordination is provided by the database lifetime
// lease; the row barrier in recovery additionally waits for an already-open
// transaction to settle.
var restoreOperationMu sync.Mutex

type restoreIntentRecord struct {
	Version int                   `json:"version"`
	ID      string                `json:"id"`
	State   string                `json:"state"`
	Swaps   []directorySwapRecord `json:"swaps"`
}

type restoreIntent struct {
	db        *storage.DB
	pending   restoreIntentRecord
	committed restoreIntentRecord
	pendingJS string
	commitJS  string
}

func newRestoreIntent(db *storage.DB, swaps []*directorySwap) (*restoreIntent, error) {
	if db == nil {
		return nil, errors.New("restore: database is nil")
	}
	records := make([]directorySwapRecord, len(swaps))
	for i, swap := range swaps {
		if swap == nil {
			return nil, errors.New("restore: nil directory snapshot")
		}
		if err := rejectSQLiteInsideRestoreTree(db, swap.dest, "restore destination"); err != nil {
			return nil, err
		}
		records[i] = swap.record()
		if err := validateDirectorySwapRecord(records[i]); err != nil {
			return nil, err
		}
		for j := 0; j < i; j++ {
			if directoriesOverlap(records[i].Dest, records[j].Dest) {
				return nil, fmt.Errorf("restore: snapshot destinations overlap: %s and %s", records[j].Dest, records[i].Dest)
			}
		}
	}
	pending := restoreIntentRecord{Version: restoreIntentVersion, ID: uuid.NewString(), State: "pending", Swaps: records}
	committed := pending
	committed.State = "committed"
	pendingJS, err := marshalRestoreIntent(pending)
	if err != nil {
		return nil, err
	}
	commitJS, err := marshalRestoreIntent(committed)
	if err != nil {
		return nil, err
	}
	return &restoreIntent{db: db, pending: pending, committed: committed, pendingJS: pendingJS, commitJS: commitJS}, nil
}

func marshalRestoreIntent(record restoreIntentRecord) (string, error) {
	raw, err := json.Marshal(record)
	return string(raw), err
}

func decodeRestoreIntent(raw string) (restoreIntentRecord, error) {
	var record restoreIntentRecord
	if err := rejectDuplicateJSONObjectKeys([]byte(raw)); err != nil {
		return record, fmt.Errorf("restore: invalid recovery intent: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return record, fmt.Errorf("restore: invalid recovery intent: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return record, errors.New("restore: invalid trailing recovery intent data")
		}
		return record, fmt.Errorf("restore: invalid trailing recovery intent data: %w", err)
	}
	if record.Version != restoreIntentVersion || record.ID == "" || (record.State != "pending" && record.State != "committed") || len(record.Swaps) == 0 {
		return record, errors.New("restore: invalid recovery intent fields")
	}
	for i, swap := range record.Swaps {
		if err := validateDirectorySwapRecord(swap); err != nil {
			return record, err
		}
		for j := 0; j < i; j++ {
			if directoriesOverlap(swap.Dest, record.Swaps[j].Dest) {
				return record, fmt.Errorf("restore: recovery destinations overlap: %s and %s", record.Swaps[j].Dest, swap.Dest)
			}
		}
	}
	return record, nil
}

func (intent *restoreIntent) Begin(ctx context.Context) error {
	if err := intent.db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	d := intent.db.Dialect()
	q := fmt.Sprintf(`INSERT INTO _settings (key,value) VALUES (%s,%s) ON CONFLICT (key) DO NOTHING`,
		d.Placeholder(1), d.Placeholder(2))
	tag, err := intent.db.Exec(ctx, q, restoreIntentKey, intent.pendingJS)
	if err != nil {
		return fmt.Errorf("restore: persist recovery intent: %w", err)
	}
	if tag.RowsAffected != 1 {
		return fmt.Errorf("%w: another restore intent already exists", ErrRestoreRecoveryRequired)
	}
	return nil
}

func (intent *restoreIntent) MarkCommitted(ctx context.Context) error {
	tag, err := updateRestoreIntentCAS(ctx, intent.db, intent.pendingJS, intent.commitJS)
	if err != nil {
		return fmt.Errorf("restore: mark recovery intent committed: %w", err)
	}
	if tag != 1 {
		return fmt.Errorf("%w: recovery intent changed before commit", ErrRestoreRecoveryRequired)
	}
	return nil
}

func updateRestoreIntentCAS(ctx context.Context, db *storage.DB, oldValue, newValue string) (int64, error) {
	d := db.Dialect()
	q := fmt.Sprintf(`UPDATE _settings SET value=%s WHERE key=%s AND value=%s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
	tag, err := db.Exec(ctx, q, newValue, restoreIntentKey, oldValue)
	return tag.RowsAffected, err
}

func (intent *restoreIntent) Rollback(ctx context.Context, swaps []*directorySwap) error {
	fileErr := rollbackDirectorySwaps(swaps)
	if fileErr != nil {
		return fileErr
	}
	return intent.deleteCAS(ctx, intent.pendingJS)
}

func (intent *restoreIntent) Finalize(ctx context.Context, swaps []*directorySwap) error {
	// Keep each retired previous tree at its recorded tombstone until the
	// committed marker deletion is itself durable. The tombstone is the evidence
	// that dest=true/stage=false means a completed publication rather than a
	// vanished Windows stage directory.
	if err := sealDirectorySwaps(swaps); err != nil {
		return err
	}
	if err := intent.deleteCAS(ctx, intent.commitJS); err != nil {
		return err
	}
	// A transactional caller must wait until its marker deletion commits before
	// removing the evidence. Recovery and commit-resolution do that explicitly.
	if storage.HasTx(ctx) {
		return nil
	}
	return cleanupCommittedDirectorySwaps(swaps)
}

func (intent *restoreIntent) deleteCAS(ctx context.Context, expected string) error {
	d := intent.db.Dialect()
	q := fmt.Sprintf(`DELETE FROM _settings WHERE key=%s AND value=%s`, d.Placeholder(1), d.Placeholder(2))
	tag, err := intent.db.Exec(ctx, q, restoreIntentKey, expected)
	if err != nil {
		return fmt.Errorf("restore: delete recovery intent: %w", err)
	}
	if tag.RowsAffected != 1 {
		return fmt.Errorf("%w: recovery intent changed during cleanup", ErrRestoreRecoveryRequired)
	}
	return nil
}

// ResolveCommitError establishes a fresh-transaction barrier before reading
// the intent. PostgreSQL can report a lost commit acknowledgement while the
// server transaction still owns the intent row lock; the no-op UPDATE waits
// until that outcome is final. SQLite similarly serializes writers.
func (intent *restoreIntent) ResolveCommitError(rootCtx context.Context, swaps []*directorySwap, commitErr error) error {
	// WithoutCancel intentionally keeps values, including the restore's pinned
	// durable session. Explicitly mask that session: the connection which just
	// returned an ambiguous Commit must never be reused to resolve its outcome.
	baseCtx := storage.WithoutDurableSession(context.WithoutCancel(rootCtx))
	ctx, cancel := context.WithTimeout(baseCtx, restoreResolveTimeout)
	defer cancel()
	// SQLite deliberately has a one-connection pool. Discard its original
	// pinned connection before asking for an independent resolution session;
	// PostgreSQL pools can acquire a second connection, but the connection with
	// the lost acknowledgement must not run even a session-setting cleanup.
	if session := storage.DurableSessionFromContext(rootCtx, intent.db); session != nil {
		if err := session.Discard(ctx); err != nil {
			return errors.Join(fmt.Errorf("restore: database commit outcome is unknown: %w", commitErr),
				fmt.Errorf("restore: discard ambiguous database session: %w", err), ErrRestoreRecoveryRequired)
		}
	}
	tx, txCtx, err := intent.db.BeginDurableTx(ctx)
	if err != nil {
		return errors.Join(fmt.Errorf("restore: database commit outcome is unknown: %w", commitErr), err, ErrRestoreRecoveryRequired)
	}
	open := true
	defer func() {
		if open {
			_ = tx.Rollback(txCtx)
		}
	}()
	d := intent.db.Dialect()
	q := fmt.Sprintf(`UPDATE _settings SET value=value WHERE key=%s`, d.Placeholder(1))
	if _, err := intent.db.Exec(txCtx, q, restoreIntentKey); err != nil {
		return errors.Join(fmt.Errorf("restore: database commit outcome is unknown: %w", commitErr), err, ErrRestoreRecoveryRequired)
	}
	raw, ok, err := readRestoreIntent(txCtx, intent.db)
	if err != nil {
		return errors.Join(fmt.Errorf("restore: database commit outcome is unknown: %w", commitErr), err, ErrRestoreRecoveryRequired)
	}
	if !ok {
		return errors.Join(commitErr, fmt.Errorf("%w: recovery intent disappeared", ErrRestoreRecoveryRequired))
	}
	state, err := decodeRestoreIntent(raw)
	if err != nil {
		return errors.Join(commitErr, ErrRestoreRecoveryRequired, err)
	}
	var resolvedErr error
	switch state.State {
	case "committed":
		if state.ID != intent.committed.ID {
			return errors.Join(commitErr, fmt.Errorf("%w: recovery intent belongs to another operation", ErrRestoreRecoveryRequired))
		}
		resolvedErr = intent.Finalize(txCtx, swaps)
	case "pending":
		if state.ID != intent.pending.ID {
			return errors.Join(commitErr, fmt.Errorf("%w: recovery intent belongs to another operation", ErrRestoreRecoveryRequired))
		}
		resolvedErr = intent.Rollback(txCtx, swaps)
	default:
		return errors.Join(commitErr, fmt.Errorf("%w: unexpected recovery state", ErrRestoreRecoveryRequired))
	}
	if resolvedErr != nil {
		return errors.Join(commitErr, resolvedErr, ErrRestoreRecoveryRequired)
	}
	if err := tx.Commit(txCtx); err != nil {
		open = false
		return errors.Join(fmt.Errorf("restore: resolution commit outcome is unknown: %w", err), commitErr, ErrRestoreRecoveryRequired)
	}
	open = false
	if state.State == "pending" {
		return commitErr
	}
	return cleanupCommittedDirectorySwaps(swaps)
}

func readRestoreIntent(ctx context.Context, db *storage.DB) (string, bool, error) {
	exists, err := tableExistsChecked(ctx, db, "_settings")
	if err != nil {
		return "", false, fmt.Errorf("restore: inspect recovery intent table: %w", err)
	}
	if !exists {
		return "", false, nil
	}
	var raw string
	err = db.QueryRow(ctx, `SELECT value FROM _settings WHERE key=`+db.Dialect().Placeholder(1), restoreIntentKey).Scan(&raw)
	if storage.IsNotFound(err) {
		return "", false, nil
	}
	return raw, err == nil, err
}

// CheckNoPendingRestore is a read-only guard for callers that open a database
// outside the normal server startup path. It deliberately does not decode or
// mutate the marker: any reserved row means the database must first be opened
// by a recovery-capable path with its trusted destination allowlist.
func CheckNoPendingRestore(ctx context.Context, db *storage.DB) error {
	_, exists, err := readRestoreIntent(ctx, db)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("%w: interrupted restore marker exists", ErrRestoreRecoveryRequired)
	}
	return nil
}

// RecoverPendingRestore resolves a durable intent before a database is served
// or another restore starts. allowedDestinations is an exact allowlist supplied
// from trusted runtime configuration; intent paths are never accepted merely
// because they share a parent.
func RecoverPendingRestore(ctx context.Context, db *storage.DB, allowedDestinations ...string) error {
	restoreOperationMu.Lock()
	defer restoreOperationMu.Unlock()
	session, err := db.BeginDurableSession(ctx)
	if err != nil {
		return fmt.Errorf("restore: begin durable recovery session: %w", err)
	}
	err = recoverPendingRestoreLocked(session.Context(), db, allowedDestinations...)
	return errors.Join(err, session.Close())
}

func recoverPendingRestoreLocked(ctx context.Context, db *storage.DB, allowedDestinations ...string) error {
	_, ok, err := readRestoreIntent(ctx, db)
	if err != nil || !ok {
		return err
	}
	allowed, err := exactDestinationAllowlist(allowedDestinations)
	if err != nil {
		return errors.Join(ErrRestoreRecoveryRequired, err)
	}

	// Hold the marker row lock through filesystem resolution and marker delete.
	// This makes concurrent recovery idempotent and waits out an import whose
	// committed marker update has not become visible yet.
	tx, txCtx, err := db.BeginDurableTx(ctx)
	if err != nil {
		return fmt.Errorf("restore: begin recovery barrier: %w", err)
	}
	open := true
	defer func() {
		if open {
			_ = tx.Rollback(txCtx)
		}
	}()
	d := db.Dialect()
	barrierQuery := fmt.Sprintf(`UPDATE _settings SET value=value WHERE key=%s`, d.Placeholder(1))
	if _, err := db.Exec(txCtx, barrierQuery, restoreIntentKey); err != nil {
		return fmt.Errorf("restore: acquire recovery barrier: %w", err)
	}
	raw, ok, err := readRestoreIntent(txCtx, db)
	if err != nil {
		return err
	}
	if !ok {
		if err := tx.Commit(txCtx); err != nil {
			open = false
			return fmt.Errorf("restore: commit empty recovery barrier: %w", err)
		}
		open = false
		return nil
	}
	record, err := decodeRestoreIntent(raw)
	if err != nil {
		return errors.Join(ErrRestoreRecoveryRequired, err)
	}
	for _, swap := range record.Swaps {
		if !allowed[canonicalDestinationKey(swap.Dest)] {
			return fmt.Errorf("%w: recovery destination is not allowed: %s", ErrRestoreRecoveryRequired, swap.Dest)
		}
		if err := rejectSQLiteInsideRestoreTree(db, swap.Dest, "recovery destination"); err != nil {
			return errors.Join(ErrRestoreRecoveryRequired, err)
		}
	}

	swaps := make([]*directorySwap, len(record.Swaps))
	for i, swap := range record.Swaps {
		swaps[i] = swapFromRecord(swap)
	}
	intent := &restoreIntent{db: db, pending: record, committed: record}
	if record.State == "pending" {
		intent.pendingJS = raw
		err = intent.Rollback(txCtx, swaps)
	} else {
		intent.commitJS = raw
		err = intent.Finalize(txCtx, swaps)
	}
	if err != nil {
		return errors.Join(ErrRestoreRecoveryRequired, err)
	}
	if err := tx.Commit(txCtx); err != nil {
		open = false
		return errors.Join(fmt.Errorf("restore: commit recovery barrier: %w", err), ErrRestoreRecoveryRequired)
	}
	open = false
	if record.State == "committed" {
		return cleanupCommittedDirectorySwaps(swaps)
	}
	return nil
}

func exactDestinationAllowlist(destinations []string) (map[string]bool, error) {
	allowed := make(map[string]bool, len(destinations))
	for _, raw := range destinations {
		if raw == "" {
			continue
		}
		abs, err := filepath.Abs(raw)
		if err != nil {
			return nil, err
		}
		allowed[canonicalDestinationKey(abs)] = true
	}
	return allowed, nil
}

func canonicalDestinationKey(path string) string {
	key := canonicalDirectoryPath(filepath.Clean(path))
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key
}

func detachedRestoreContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), restoreOperationWindow)
}
