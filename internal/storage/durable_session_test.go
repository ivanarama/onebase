package storage

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestSetPostgresSynchronousCommitUsesParameterizedSessionAndLocalSettings(t *testing.T) {
	type call struct {
		query string
		args  []any
	}
	var calls []call
	exec := func(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
		calls = append(calls, call{query: query, args: append([]any(nil), args...)})
		return pgconn.NewCommandTag("SELECT 1"), nil
	}
	if err := setPostgresSynchronousCommit(context.Background(), exec, "on", false); err != nil {
		t.Fatal(err)
	}
	if err := setPostgresSynchronousCommit(context.Background(), exec, "remote_apply", true); err != nil {
		t.Fatal(err)
	}
	want := []call{
		{query: "SELECT set_config('synchronous_commit', $1, false)", args: []any{"on"}},
		{query: "SELECT set_config('synchronous_commit', $1, true)", args: []any{"remote_apply"}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("set_config calls = %#v, want %#v", calls, want)
	}
}

func TestSetPostgresSynchronousCommitRejectsEmptyModeWithoutExecuting(t *testing.T) {
	called := false
	exec := func(context.Context, string, ...any) (pgconn.CommandTag, error) {
		called = true
		return pgconn.CommandTag{}, nil
	}
	if err := setPostgresSynchronousCommit(context.Background(), exec, " ", false); err == nil {
		t.Fatal("empty synchronous_commit mode was accepted")
	}
	if called {
		t.Fatal("executor called for invalid mode")
	}
}

type staticPGRow struct {
	value string
	err   error
}

type failingDurableTx struct{ err error }

func (tx failingDurableTx) Exec(context.Context, string, ...any) (CommandTag, error) {
	return CommandTag{}, nil
}
func (tx failingDurableTx) Query(context.Context, string, ...any) (Rows, error) { return nil, nil }
func (tx failingDurableTx) QueryRow(context.Context, string, ...any) Row        { return errorRow{} }
func (tx failingDurableTx) Commit(context.Context) error                        { return tx.err }
func (tx failingDurableTx) Rollback(context.Context) error                      { return tx.err }

func (row staticPGRow) Scan(dest ...any) error {
	if row.err != nil {
		return row.err
	}
	*(dest[0].(*string)) = row.value
	return nil
}

func TestVerifyPostgresSynchronousCommitFailsClosedOnDowngrade(t *testing.T) {
	query := func(_ context.Context, sql string, _ ...any) pgx.Row {
		if sql != "SHOW synchronous_commit" {
			t.Fatalf("query = %q", sql)
		}
		return staticPGRow{value: "off"}
	}
	if err := verifyPostgresSynchronousCommit(context.Background(), query); err == nil {
		t.Fatal("synchronous_commit=off was accepted at commit boundary")
	}
	query = func(context.Context, string, ...any) pgx.Row { return staticPGRow{value: "on"} }
	if err := verifyPostgresSynchronousCommit(context.Background(), query); err != nil {
		t.Fatalf("synchronous_commit=on rejected: %v", err)
	}
}

func TestClosedDurableSessionContextFailsClosed(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "closed-session.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	session, err := db.BeginDurableSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessionCtx := session.Context()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(sessionCtx, "CREATE TABLE must_not_run (id INTEGER)"); err == nil ||
		!errors.Is(err, ErrDurableSessionClosed) {
		t.Fatalf("Exec with closed durable context error = %v", err)
	}
	if _, _, err := db.BeginTx(sessionCtx); err == nil || !errors.Is(err, ErrDurableSessionClosed) {
		t.Fatalf("BeginTx with closed durable context error = %v", err)
	}
}

func TestWithoutDurableSessionForcesIndependentConnection(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "fresh-session.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	first, err := db.BeginDurableSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	masked := WithoutDurableSession(context.WithoutCancel(first.Context()))
	if got := durableSessionFromContext(masked, db); got != nil {
		t.Fatal("WithoutDurableSession retained the original pinned session")
	}
	if err := first.Discard(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := db.BeginDurableSession(masked)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close() //nolint:errcheck // asserted through explicit operations below
	if first.sqliteConn == second.sqliteConn {
		t.Fatal("independent durable session reused the original sql.Conn wrapper")
	}
	if err := second.ensureDurable(second.Context()); err != nil {
		t.Fatalf("fresh durable session is unusable: %v", err)
	}
	var foreignKeys int
	if err := second.sqliteConn.QueryRowContext(second.Context(), "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("replacement connection foreign_keys = %d, want 1", foreignKeys)
	}
}

func TestSQLiteFKCleanupSurvivesDiscardedDurableSession(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "discard-fk.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	session, err := db.BeginDurableSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cleanup, err := db.DisableFKForImport(session.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Discard(ctx); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("FK cleanup after session discard: %v", err)
	}
	var foreignKeys int
	if err := db.QueryRow(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("replacement connection foreign_keys = %d, want 1", foreignKeys)
	}
}

func TestOwnedDurableTransactionDiscardsSessionOnOutcomeError(t *testing.T) {
	for _, operation := range []string{"commit", "rollback"} {
		t.Run(operation, func(t *testing.T) {
			ctx := context.Background()
			db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), operation+".db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(db.Close)
			session, err := db.BeginDurableSession(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := errors.New("ambiguous transaction outcome")
			tx := &ownedDurableSessionTx{Tx: failingDurableTx{err: want}, session: session}
			if operation == "commit" {
				err = tx.Commit(session.Context())
			} else {
				err = tx.Rollback(session.Context())
			}
			if !errors.Is(err, want) {
				t.Fatalf("%s error = %v, want outcome error", operation, err)
			}
			if _, err := db.Exec(session.Context(), "CREATE TABLE must_not_run (id INTEGER)"); !errors.Is(err, ErrDurableSessionClosed) {
				t.Fatalf("%s left ambiguous session usable: %v", operation, err)
			}
		})
	}
}
