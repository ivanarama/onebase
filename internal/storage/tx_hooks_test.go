package storage

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

func openTxHooksTestDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "hooks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db, ctx
}

func TestTxHooks_CommitAndRollback(t *testing.T) {
	db, ctx := openTxHooksTestDB(t)

	var committed []string
	tx, txCtx, err := db.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	DeferUntilTxCommit(txCtx, func() { committed = append(committed, "commit") })
	DeferUntilTxRollback(txCtx, func() { committed = append(committed, "rollback") })
	if err := tx.Commit(txCtx); err != nil {
		t.Fatal(err)
	}
	if want := []string{"commit"}; !reflect.DeepEqual(committed, want) {
		t.Fatalf("commit callbacks = %v, want %v", committed, want)
	}

	var rolledBack []string
	tx, txCtx, err = db.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	DeferUntilTxCommit(txCtx, func() { rolledBack = append(rolledBack, "commit") })
	DeferUntilTxRollback(txCtx, func() { rolledBack = append(rolledBack, "rollback") })
	if err := tx.Rollback(txCtx); err != nil {
		t.Fatal(err)
	}
	if want := []string{"rollback"}; !reflect.DeepEqual(rolledBack, want) {
		t.Fatalf("rollback callbacks = %v, want %v", rolledBack, want)
	}
}

func TestTxHooks_SavepointScopes(t *testing.T) {
	db, ctx := openTxHooksTestDB(t)
	tx, txCtx, err := db.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(txCtx) //nolint:errcheck

	var events []string
	DeferUntilTxCommit(txCtx, func() { events = append(events, "outer-commit") })
	DeferUntilTxRollback(txCtx, func() { events = append(events, "outer-rollback") })

	PushTxHookScope(txCtx)
	DeferUntilTxCommit(txCtx, func() { events = append(events, "inner-commit-discarded") })
	DeferUntilTxRollback(txCtx, func() { events = append(events, "inner-rollback") })
	RollbackTxHookScope(txCtx)
	if want := []string{"inner-rollback"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("savepoint rollback callbacks = %v, want %v", events, want)
	}

	PushTxHookScope(txCtx)
	DeferUntilTxCommit(txCtx, func() { events = append(events, "released-commit") })
	DeferUntilTxRollback(txCtx, func() { events = append(events, "released-rollback") })
	CommitTxHookScope(txCtx)
	if err := tx.Commit(txCtx); err != nil {
		t.Fatal(err)
	}
	if want := []string{"inner-rollback", "outer-commit", "released-commit"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("savepoint commit callbacks = %v, want %v", events, want)
	}
}

func TestTxHooks_BeforeCommitFailureRollsBack(t *testing.T) {
	db, ctx := openTxHooksTestDB(t)
	if _, err := db.Exec(ctx, `CREATE TABLE guarded_commit (value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	guardErr := errors.New("commit invariant failed")
	var events []string
	err := db.WithTx(ctx, func(txCtx context.Context) error {
		if _, err := db.Exec(txCtx, `INSERT INTO guarded_commit (value) VALUES ('transient')`); err != nil {
			return err
		}
		if !DeferBeforeTxCommit(txCtx, func() error { return guardErr }) {
			t.Fatal("before-commit hook was not registered")
		}
		DeferUntilTxCommit(txCtx, func() { events = append(events, "commit") })
		DeferUntilTxRollback(txCtx, func() { events = append(events, "rollback") })
		return nil
	})
	if !errors.Is(err, guardErr) {
		t.Fatalf("WithTx error = %v, want guard error", err)
	}
	if want := []string{"rollback"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("guard callback outcome = %v, want %v", events, want)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM guarded_commit`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("before-commit failure persisted %d rows, want 0", count)
	}
}

func TestTxScope_CanceledDerivedContextKeepsLifecycleSafe(t *testing.T) {
	db, ctx := openTxHooksTestDB(t)
	entity := &metadata.Entity{
		Name: "CanceledScopeLifecycle",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Name", Type: metadata.FieldTypeString, Required: true},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatal(err)
	}

	t.Run("error path rolls back savepoint with detached cleanup context", func(t *testing.T) {
		tx, txCtx, err := db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(txCtx) //nolint:errcheck

		id := uuid.New()
		derivedCtx, cancel := context.WithCancel(txCtx)
		sentinel := errors.New("scope callback failed")
		scopeErr := db.WithTxScope(derivedCtx, func(scopeCtx context.Context) error {
			if err := db.UpsertProvisional(scopeCtx, entity.Name, id,
				map[string]any{"Name": ""}, entity); err != nil {
				return err
			}
			cancel()
			return sentinel
		})
		if !errors.Is(scopeErr, sentinel) {
			t.Fatalf("nested scope error = %v, want callback sentinel", scopeErr)
		}
		if err := tx.Commit(txCtx); err != nil {
			t.Fatalf("outer commit after successful savepoint rollback: %v", err)
		}
		if _, err := db.GetByID(ctx, entity.Name, id, entity); !IsNotFound(err) {
			t.Fatalf("canceled error scope leaked provisional row: %v", err)
		}
	})

	t.Run("success path inherits guard before outer commit", func(t *testing.T) {
		tx, txCtx, err := db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(txCtx) //nolint:errcheck

		id := uuid.New()
		derivedCtx, cancel := context.WithCancel(txCtx)
		scopeErr := db.WithTxScope(derivedCtx, func(scopeCtx context.Context) error {
			if err := db.UpsertProvisional(scopeCtx, entity.Name, id,
				map[string]any{"Name": ""}, entity); err != nil {
				return err
			}
			cancel()
			return nil
		})
		if scopeErr != nil {
			t.Fatalf("release savepoint with canceled derived context: %v", scopeErr)
		}
		if err := tx.Commit(txCtx); !errors.Is(err, ErrIncompleteWriteLifecycle) {
			t.Fatalf("outer commit = %v, want ErrIncompleteWriteLifecycle", err)
		}
		if _, err := db.GetByID(ctx, entity.Name, id, entity); !IsNotFound(err) {
			t.Fatalf("canceled success scope leaked provisional row: %v", err)
		}
	})
}
