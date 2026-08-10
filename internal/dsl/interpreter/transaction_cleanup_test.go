package interpreter_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/storage"
)

type cleanupFakeDB struct {
	events *[]string
	tx     *cleanupFakeTx
}

func (db *cleanupFakeDB) BeginTx(ctx context.Context) (storage.Tx, context.Context, error) {
	db.tx = &cleanupFakeTx{events: db.events}
	return db.tx, ctx, nil
}

func (db *cleanupFakeDB) Exec(ctx context.Context, sql string, _ ...any) (storage.CommandTag, error) {
	if err := ctx.Err(); err != nil {
		return storage.CommandTag{}, fmt.Errorf("cleanup used canceled context: %w", err)
	}
	*db.events = append(*db.events, sql)
	return storage.CommandTag{}, nil
}

type cleanupFakeTx struct{ events *[]string }

func (tx *cleanupFakeTx) Exec(context.Context, string, ...any) (storage.CommandTag, error) {
	return storage.CommandTag{}, nil
}
func (tx *cleanupFakeTx) Query(context.Context, string, ...any) (storage.Rows, error) {
	return nil, nil
}
func (tx *cleanupFakeTx) QueryRow(context.Context, string, ...any) storage.Row {
	return cleanupFakeRow{}
}
func (tx *cleanupFakeTx) Commit(context.Context) error { return nil }
func (tx *cleanupFakeTx) Rollback(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("cleanup used canceled context: %w", err)
	}
	*tx.events = append(*tx.events, "ROLLBACK TRANSACTION")
	return nil
}

type cleanupFakeRow struct{}

func (cleanupFakeRow) Scan(...any) error { return nil }

func callTxBuiltin(t *testing.T, funcs map[string]any, name string) {
	t.Helper()
	fn, ok := funcs[name].(interpreter.BuiltinFunc)
	if !ok {
		t.Fatalf("builtin %s has type %T", name, funcs[name])
	}
	if _, err := fn(nil, "cleanup_test.os", 1); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func TestTxStateRollbackOpenUsesDetachedContextAndInnerToOuterOrder(t *testing.T) {
	executionCtx, cancelExecution := context.WithCancel(context.Background())
	events := []string{}
	db := &cleanupFakeDB{events: &events}
	state := interpreter.NewTxState(executionCtx)
	funcs := interpreter.NewTxFunctions(state, db)
	callTxBuiltin(t, funcs, "BeginTransaction")
	callTxBuiltin(t, funcs, "BeginTransaction")
	events = events[:0]

	cancelExecution()
	cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(state.Ctx()), time.Second)
	defer cancelCleanup()
	if err := state.RollbackOpen(cleanupCtx); err != nil {
		t.Fatalf("RollbackOpen: %v", err)
	}
	want := []string{"ROLLBACK TO SAVEPOINT sp1", "RELEASE SAVEPOINT sp1", "ROLLBACK TRANSACTION"}
	if strings.Join(events, "\n") != strings.Join(want, "\n") {
		t.Fatalf("cleanup order = %q, want %q", events, want)
	}
	if state.HasOpen() {
		t.Fatal("transaction state remained open after cleanup")
	}
}

type recordingTxDB struct {
	db     *storage.DB
	events *[]string
}

func (r *recordingTxDB) BeginTx(ctx context.Context) (storage.Tx, context.Context, error) {
	tx, txCtx, err := r.db.BeginTx(ctx)
	if err != nil {
		return nil, nil, err
	}
	return &recordingTx{Tx: tx, events: r.events}, txCtx, nil
}

func (r *recordingTxDB) Exec(ctx context.Context, sql string, args ...any) (storage.CommandTag, error) {
	tag, err := r.db.Exec(ctx, sql, args...)
	if err == nil && (strings.HasPrefix(sql, "ROLLBACK TO SAVEPOINT") || strings.HasPrefix(sql, "RELEASE SAVEPOINT")) {
		*r.events = append(*r.events, sql)
	}
	return tag, err
}

type recordingTx struct {
	storage.Tx
	events *[]string
}

func (tx *recordingTx) Rollback(ctx context.Context) error {
	*tx.events = append(*tx.events, "outer rollback start")
	err := tx.Tx.Rollback(ctx)
	*tx.events = append(*tx.events, "outer rollback done")
	return err
}

func TestTxStateRollbackOpenRollsBackDataAndRunsHooksAfterDBCleanup(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx, cancelExecution := context.WithCancel(context.Background())
		defer cancelExecution()
		if _, err := db.Exec(ctx, `CREATE TABLE tx_cleanup_items (name TEXT NOT NULL)`); err != nil {
			t.Fatalf("create table: %v", err)
		}

		events := []string{}
		recordingDB := &recordingTxDB{db: db, events: &events}
		state := interpreter.NewTxState(ctx)
		funcs := interpreter.NewTxFunctions(state, recordingDB)
		callTxBuiltin(t, funcs, "BeginTransaction")
		if !storage.DeferUntilTxRollback(state.Ctx(), func() { events = append(events, "outer hook") }) {
			t.Fatal("outer rollback hook was not registered")
		}
		insert := `INSERT INTO tx_cleanup_items(name) VALUES (` + db.Dialect().Placeholder(1) + `)`
		if _, err := db.Exec(state.Ctx(), insert, "outer"); err != nil {
			t.Fatalf("insert outer: %v", err)
		}
		callTxBuiltin(t, funcs, "BeginTransaction")
		if !storage.DeferUntilTxRollback(state.Ctx(), func() { events = append(events, "inner hook") }) {
			t.Fatal("inner rollback hook was not registered")
		}
		if _, err := db.Exec(state.Ctx(), insert, "inner"); err != nil {
			t.Fatalf("insert inner: %v", err)
		}
		events = events[:0]

		cancelExecution()
		cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(state.Ctx()), 5*time.Second)
		if err := state.RollbackOpen(cleanupCtx); err != nil {
			cancelCleanup()
			t.Fatalf("RollbackOpen: %v", err)
		}
		cancelCleanup()

		want := []string{
			"ROLLBACK TO SAVEPOINT sp1",
			"RELEASE SAVEPOINT sp1",
			"inner hook",
			"outer rollback start",
			"outer hook",
			"outer rollback done",
		}
		if strings.Join(events, "\n") != strings.Join(want, "\n") {
			t.Fatalf("cleanup/hook order = %q, want %q", events, want)
		}
		var count int
		if err := db.QueryRow(context.Background(), `SELECT COUNT(*) FROM tx_cleanup_items`).Scan(&count); err != nil {
			t.Fatalf("count rows: %v", err)
		}
		if count != 0 {
			t.Fatalf("cleanup left %d rows", count)
		}
	})
}
