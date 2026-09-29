package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestReadOnlyContextRejectsWriteThroughStorageEntrypoints(t *testing.T) {
	db, err := ConnectSQLite(context.Background(), filepath.Join(t.TempDir(), "readonly.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(context.Background(), `CREATE TABLE probe (value TEXT)`); err != nil {
		t.Fatal(err)
	}
	ctx := ReadOnlyContext(context.Background())
	if _, err := db.Exec(ctx, `INSERT INTO probe (value) VALUES ('x')`); !errors.Is(err, ErrReadOnlyContext) {
		t.Fatalf("Exec write: %v", err)
	}
	if _, err := db.Query(ctx, `INSERT INTO probe (value) VALUES ('x') RETURNING value`); !errors.Is(err, ErrReadOnlyContext) {
		t.Fatalf("Query write: %v", err)
	}
	var value string
	if err := db.QueryRow(ctx, `INSERT INTO probe (value) VALUES ('x') RETURNING value`).Scan(&value); !errors.Is(err, ErrReadOnlyContext) {
		t.Fatalf("QueryRow write: %v", err)
	}
	if _, err := db.Query(ctx, `SELECT value FROM probe; DELETE FROM probe`); !errors.Is(err, ErrReadOnlyContext) {
		t.Fatalf("multiple statements: %v", err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM probe`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("read-only SELECT: count=%d err=%v", count, err)
	}
}
