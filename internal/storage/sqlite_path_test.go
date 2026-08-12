package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSQLitePathReportsOnlyOnDiskDatabase(t *testing.T) {
	ctx := context.Background()
	relative := filepath.Join(t.TempDir(), "nested", "onebase.db")
	db, err := ConnectSQLite(ctx, relative)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(relative)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if got := db.SQLitePath(); got != want {
		db.Close()
		t.Fatalf("SQLitePath() = %q, want %q", got, want)
	}
	db.Close()

	memoryDB, err := ConnectSQLite(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer memoryDB.Close()
	if got := memoryDB.SQLitePath(); got != "" {
		t.Fatalf("in-memory SQLitePath() = %q, want empty", got)
	}
}
