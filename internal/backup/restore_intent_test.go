package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/storage"
)

func TestUniversalBackupRejectsSQLiteDatabaseInsideFileConfigTree(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(configDir, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	if err := ExportUniversal(ctx, db, "file", configDir, "", "base", io.Discard); err == nil ||
		!strings.Contains(err.Error(), "SQLite database") {
		t.Fatalf("ExportUniversal error = %v, want SQLite/config overlap rejection", err)
	}
	emptyArchive := bytes.NewReader(nil)
	if _, err := ImportUniversal(ctx, db, "file", configDir, "", emptyArchive, 0); err == nil ||
		!strings.Contains(err.Error(), "SQLite database") {
		t.Fatalf("ImportUniversal error = %v, want SQLite/config overlap rejection", err)
	}
}

func TestUniversalRestoreRejectsSQLiteDatabaseInsideAttachmentTree(t *testing.T) {
	ctx := context.Background()
	attachmentsDir := t.TempDir()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(attachmentsDir, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetFilesDir(attachmentsDir)
	t.Cleanup(db.Close)

	emptyArchive := bytes.NewReader(nil)
	if _, err := ImportUniversal(ctx, db, "database", "", attachmentsDir, emptyArchive, 0); err == nil ||
		!strings.Contains(err.Error(), "SQLite database") {
		t.Fatalf("ImportUniversal error = %v, want SQLite/attachment overlap rejection", err)
	}
	if _, err := DemoReset(ctx, db, filepath.Join(t.TempDir(), "missing.obz")); err == nil ||
		!strings.Contains(err.Error(), "SQLite database") {
		t.Fatalf("DemoReset error = %v, want SQLite/attachment overlap rejection", err)
	}
}

func TestDetachedRestoreContextSurvivesCallerCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	opCtx, cancelOperation := detachedRestoreContext(parent)
	defer cancelOperation()
	cancelParent()
	if err := parent.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("parent error = %v", err)
	}
	if err := opCtx.Err(); err != nil {
		t.Fatalf("detached operation was canceled with caller: %v", err)
	}
}

func TestRecoverPendingRestoreRollsBackEveryPublishedCutPoint(t *testing.T) {
	tests := []struct {
		name       string
		hadDest    bool
		cutRestore func(*testing.T, *directorySwap)
	}{
		{name: "prepared", hadDest: true},
		{name: "old tree renamed", hadDest: true, cutRestore: func(t *testing.T, swap *directorySwap) {
			t.Helper()
			if err := os.Rename(swap.dest, swap.backup); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "new tree published", hadDest: true, cutRestore: func(t *testing.T, swap *directorySwap) {
			t.Helper()
			if err := swap.Publish(); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "absent destination prepared", hadDest: false},
		{name: "absent destination published", hadDest: false, cutRestore: func(t *testing.T, swap *directorySwap) {
			t.Helper()
			if err := swap.Publish(); err != nil {
				t.Fatal(err)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newSQLite(t, "pending-cut")
			swap := prepareRestoreIntentSwap(t, tt.hadDest)
			intent := beginRestoreIntentForTest(t, db, swap)
			_ = intent
			if tt.cutRestore != nil {
				tt.cutRestore(t, swap)
			}

			if err := RecoverPendingRestore(context.Background(), db, swap.dest); err != nil {
				t.Fatalf("RecoverPendingRestore: %v", err)
			}
			assertPendingTreeRestored(t, swap, tt.hadDest)
			assertNoRestoreIntent(t, db)
			// A crash after file cleanup but before the next startup check must be
			// harmless: no marker means the second pass is a no-op.
			if err := RecoverPendingRestore(context.Background(), db, swap.dest); err != nil {
				t.Fatalf("idempotent RecoverPendingRestore: %v", err)
			}
		})
	}
}

func TestRecoverCommittedRestoreFinalizesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "committed-cut")
	swap := prepareRestoreIntentSwap(t, true)
	intent := beginRestoreIntentForTest(t, db, swap)

	tx, txCtx, err := db.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := intent.MarkCommitted(txCtx); err != nil {
		_ = tx.Rollback(txCtx)
		t.Fatal(err)
	}
	if err := swap.Publish(); err != nil {
		_ = tx.Rollback(txCtx)
		t.Fatal(err)
	}
	if err := tx.Commit(txCtx); err != nil {
		t.Fatal(err)
	}

	if err := RecoverPendingRestore(ctx, db, swap.dest); err != nil {
		t.Fatalf("RecoverPendingRestore: %v", err)
	}
	if got := readFileForSwapTest(t, filepath.Join(swap.dest, "new")); got != "new" {
		t.Fatalf("committed content = %q", got)
	}
	assertPathAbsent(t, filepath.Join(swap.dest, "old"))
	assertPathAbsent(t, swap.stage)
	assertPathAbsent(t, swap.backup)
	assertNoRestoreIntent(t, db)
	if err := RecoverPendingRestore(ctx, db, swap.dest); err != nil {
		t.Fatalf("idempotent RecoverPendingRestore: %v", err)
	}
}

func TestRecoverCommittedRestoreFailsClosedWhenStageAndBackupAreMissing(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "committed-missing-stage")
	swap := prepareRestoreIntentSwap(t, true)
	intent := beginRestoreIntentForTest(t, db, swap)
	if err := intent.MarkCommitted(ctx); err != nil {
		t.Fatal(err)
	}
	// Simulate loss of an unpersisted Windows stage entry before any live-tree
	// rename. The old destination is still present, but that shape is otherwise
	// identical to a fully cleaned committed restore.
	if err := os.RemoveAll(swap.stage); err != nil {
		t.Fatal(err)
	}

	err := RecoverPendingRestore(ctx, db, swap.dest)
	if !errors.Is(err, ErrRestoreRecoveryRequired) || !strings.Contains(err.Error(), "ambiguous finalized state") {
		t.Fatalf("RecoverPendingRestore error = %v, want fail-closed ambiguous recovery", err)
	}
	if got := readFileForSwapTest(t, filepath.Join(swap.dest, "old")); got != "old" {
		t.Fatalf("failed recovery mutated old tree: %q", got)
	}
	assertRestoreIntentState(t, db, "committed")
}

func TestRestoreIntentLifecycleUsesPinnedSQLiteFullSynchronous(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "intent-durable-session")
	swap := prepareRestoreIntentSwap(t, true)
	session, err := db.BeginDurableSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessionCtx := session.Context()
	assertFull := func(boundary string, queryCtx context.Context) {
		t.Helper()
		var mode int
		if err := db.QueryRow(queryCtx, "PRAGMA synchronous").Scan(&mode); err != nil {
			t.Fatal(err)
		}
		if mode != 2 {
			t.Fatalf("synchronous at %s = %d, want FULL (2)", boundary, mode)
		}
	}
	intent, err := newRestoreIntent(db, []*directorySwap{swap})
	if err != nil {
		t.Fatal(err)
	}
	assertFull("before intent begin", sessionCtx)
	if err := intent.Begin(sessionCtx); err != nil {
		t.Fatal(err)
	}
	assertFull("after pending marker", sessionCtx)
	tx, txCtx, err := db.BeginTx(sessionCtx)
	if err != nil {
		t.Fatal(err)
	}
	if err := intent.MarkCommitted(txCtx); err != nil {
		_ = tx.Rollback(txCtx)
		t.Fatal(err)
	}
	assertFull("committed marker transaction", txCtx)
	if err := swap.Publish(); err != nil {
		_ = tx.Rollback(txCtx)
		t.Fatal(err)
	}
	if err := tx.Commit(txCtx); err != nil {
		t.Fatal(err)
	}
	assertFull("before final marker delete", sessionCtx)
	if err := intent.Finalize(sessionCtx, []*directorySwap{swap}); err != nil {
		t.Fatal(err)
	}
	assertFull("after final marker delete", sessionCtx)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	var restored int
	if err := db.QueryRow(ctx, "PRAGMA synchronous").Scan(&restored); err != nil {
		t.Fatal(err)
	}
	if restored != 1 {
		t.Fatalf("synchronous after restore lifecycle = %d, want NORMAL (1)", restored)
	}
}

func TestRestoreIntentCASAndDestinationAllowlistFailClosed(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "intent-cas")
	swap := prepareRestoreIntentSwap(t, true)
	first := beginRestoreIntentForTest(t, db, swap)

	second, err := newRestoreIntent(db, []*directorySwap{swap})
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Begin(ctx); !errors.Is(err, ErrRestoreRecoveryRequired) {
		t.Fatalf("second Begin error = %v, want ErrRestoreRecoveryRequired", err)
	}
	raw, ok, err := readRestoreIntent(ctx, db)
	if err != nil || !ok {
		t.Fatalf("read first intent: ok=%t err=%v", ok, err)
	}
	if raw != first.pendingJS {
		t.Fatal("second Begin replaced the active recovery marker")
	}

	foreign := filepath.Join(t.TempDir(), "foreign")
	if err := RecoverPendingRestore(ctx, db, foreign); !errors.Is(err, ErrRestoreRecoveryRequired) {
		t.Fatalf("foreign destination error = %v, want ErrRestoreRecoveryRequired", err)
	}
	if got := readFileForSwapTest(t, filepath.Join(swap.dest, "old")); got != "old" {
		t.Fatalf("foreign recovery mutated destination: %q", got)
	}
	if _, ok, err := readRestoreIntent(ctx, db); err != nil || !ok {
		t.Fatalf("foreign recovery removed marker: ok=%t err=%v", ok, err)
	}

	if err := RecoverPendingRestore(ctx, db, swap.dest); err != nil {
		t.Fatalf("cleanup recovery: %v", err)
	}
}

func TestCheckNoPendingRestoreIsReadOnlyAndFailClosed(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "check-pending")
	if err := CheckNoPendingRestore(ctx, db); err != nil {
		t.Fatalf("empty database check: %v", err)
	}
	swap := prepareRestoreIntentSwap(t, true)
	intent := beginRestoreIntentForTest(t, db, swap)
	if err := CheckNoPendingRestore(ctx, db); !errors.Is(err, ErrRestoreRecoveryRequired) {
		t.Fatalf("pending check error = %v, want ErrRestoreRecoveryRequired", err)
	}
	raw, ok, err := readRestoreIntent(ctx, db)
	if err != nil || !ok || raw != intent.pendingJS {
		t.Fatalf("read-only check mutated marker: ok=%t err=%v raw=%q", ok, err, raw)
	}
	if err := RecoverPendingRestore(ctx, db, swap.dest); err != nil {
		t.Fatal(err)
	}
	if err := CheckNoPendingRestore(ctx, db); err != nil {
		t.Fatalf("post-recovery check: %v", err)
	}
}

func TestResolveCommitErrorUsesFreshContextAndFinalizesLostAcknowledgement(t *testing.T) {
	rootCtx := context.Background()
	db := newSQLite(t, "lost-ack")
	durableSession, err := db.BeginDurableSession(rootCtx)
	if err != nil {
		t.Fatal(err)
	}
	defer durableSession.Close() //nolint:errcheck // resolution discards it; Close is idempotent
	rootCtx = durableSession.Context()
	if _, err := db.Exec(rootCtx, `CREATE TABLE business_state (id INTEGER PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	swap := prepareRestoreIntentSwap(t, true)
	intent, err := newRestoreIntent(db, []*directorySwap{swap})
	if err != nil {
		t.Fatal(err)
	}
	if err := intent.Begin(rootCtx); err != nil {
		t.Fatal(err)
	}

	realTx, txCtx, err := db.BeginTx(rootCtx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(txCtx, `INSERT INTO business_state (id,value) VALUES (1,'committed')`); err != nil {
		_ = realTx.Rollback(txCtx)
		t.Fatal(err)
	}
	if err := intent.MarkCommitted(txCtx); err != nil {
		_ = realTx.Rollback(txCtx)
		t.Fatal(err)
	}
	if err := swap.Publish(); err != nil {
		_ = realTx.Rollback(txCtx)
		t.Fatal(err)
	}
	lostAck := errors.New("simulated lost commit acknowledgement")
	tx := &lostAcknowledgementTx{Tx: realTx, err: lostAck}
	if err := tx.Commit(txCtx); !errors.Is(err, lostAck) {
		t.Fatalf("Commit error = %v, want lost acknowledgement", err)
	}

	// txCtx belongs to a completed transaction; resolution must use rootCtx and
	// establish a fresh transaction barrier before trusting the marker state.
	if err := intent.ResolveCommitError(rootCtx, []*directorySwap{swap}, lostAck); err != nil {
		t.Fatalf("ResolveCommitError: %v", err)
	}
	if _, err := db.Exec(rootCtx, `CREATE TABLE must_not_reuse_ambiguous_session (id INTEGER)`); !errors.Is(err, storage.ErrDurableSessionClosed) {
		t.Fatalf("original durable context remains usable after ambiguous commit: %v", err)
	}
	var value string
	if err := db.QueryRow(context.Background(), `SELECT value FROM business_state WHERE id=1`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "committed" {
		t.Fatalf("business value = %q", value)
	}
	if got := readFileForSwapTest(t, filepath.Join(swap.dest, "new")); got != "new" {
		t.Fatalf("filesystem value = %q", got)
	}
	assertNoRestoreIntent(t, db)
}

type lostAcknowledgementTx struct {
	storage.Tx
	err error
}

func (tx *lostAcknowledgementTx) Commit(ctx context.Context) error {
	if err := tx.Tx.Commit(ctx); err != nil {
		return err
	}
	return tx.err
}

func prepareRestoreIntentSwap(t *testing.T, hadDest bool) *directorySwap {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "archive")
	dest := filepath.Join(root, "live")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "new"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if hadDest {
		if err := os.MkdirAll(dest, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dest, "old"), []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	swap, err := prepareDirectorySwap(context.Background(), src, dest, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	return swap
}

func beginRestoreIntentForTest(t *testing.T, db *storage.DB, swap *directorySwap) *restoreIntent {
	t.Helper()
	intent, err := newRestoreIntent(db, []*directorySwap{swap})
	if err != nil {
		t.Fatal(err)
	}
	if err := intent.Begin(context.Background()); err != nil {
		t.Fatal(err)
	}
	return intent
}

func assertPendingTreeRestored(t *testing.T, swap *directorySwap, hadDest bool) {
	t.Helper()
	if hadDest {
		if got := readFileForSwapTest(t, filepath.Join(swap.dest, "old")); got != "old" {
			t.Fatalf("restored content = %q", got)
		}
		assertPathAbsent(t, filepath.Join(swap.dest, "new"))
	} else {
		assertPathAbsent(t, swap.dest)
	}
	assertPathAbsent(t, swap.stage)
	if swap.backup != "" {
		assertPathAbsent(t, swap.backup)
	}
}

func assertNoRestoreIntent(t *testing.T, db *storage.DB) {
	t.Helper()
	_, ok, err := readRestoreIntent(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("recovery marker still exists")
	}
}

func assertPathAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("path %s still exists: %v", path, err)
	}
}
