package storage_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/storage"
)

func TestProbeSQLiteSchemaRevisionDoesNotCreateMissingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	state, err := storage.ProbeSQLiteSchemaRevision(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if state.TableExists || state.Known {
		t.Fatalf("missing database state = %+v, want legacy/unknown", state)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only probe created the database: %v", err)
	}
}

func TestPrepareSQLiteSchemaRevisionIsAtomicAndKeepsJournalMode(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "prepare.db")
	raw, err := sql.Open("sqlite", filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `CREATE TABLE seed (id INTEGER PRIMARY KEY)`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `PRAGMA journal_mode=DELETE`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	state, err := storage.PrepareSQLiteSchemaRevision(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Known || state.Revision != storage.SchemaRevision {
		t.Fatalf("prepared state = %+v, want revision %d", state, storage.SchemaRevision)
	}
	if err := state.Check(); err != nil {
		t.Fatalf("prepared state rejected: %v", err)
	}
	raw, err = sql.Open("sqlite", filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := raw.Close(); err != nil {
			t.Errorf("close raw SQLite database: %v", err)
		}
	}()
	var mode string
	if err := raw.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "delete") {
		t.Fatalf("revision prepare changed journal mode to %q, want delete", mode)
	}
	var rows int
	if err := raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM _schema_revision WHERE id=1`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("marker singleton rows = %d, want 1", rows)
	}
}
