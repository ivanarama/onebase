package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestDemoResetSQLiteRestoresForeignKeysAfterRollback(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "demo-reset-fk-rollback")
	if _, err := db.Exec(ctx, `CREATE TABLE parent (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `CREATE TABLE child (
		id TEXT PRIMARY KEY,
		parent_id TEXT NOT NULL REFERENCES parent(id)
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO parent(id) VALUES ('parent')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO child(id,parent_id) VALUES ('child','parent')`); err != nil {
		t.Fatal(err)
	}
	if err := configdb.New(db).EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	archivePath := writeFailingDemoResetArchive(t)
	_, err := DemoReset(ctx, db, archivePath)
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("DemoReset error = %v, want primary data import error", err)
	}

	var foreignKeys int
	if err := db.QueryRow(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("read PRAGMA foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("PRAGMA foreign_keys = %d, want 1 after rollback", foreignKeys)
	}
	assertDemoResetRowsSurvived(t, ctx, db)
}

func writeFailingDemoResetArchive(t *testing.T) string {
	t.Helper()
	archive := buildUniversalAtomicFixture(t, map[string]string{
		"META.txt":            "onebase_full_export\nversion=2\nformat=universal\nhas_exchange_state=false\n",
		"manifest.json":       `{"data/ghost.jsonl":1}`,
		"config/onebase.yaml": "name: replacement\n",
		"data/ghost.jsonl":    "{\"_schema\":1}\n{\"id\":\"not-importable\"}\n",
	})
	path := filepath.Join(t.TempDir(), "failing-demo-reset.obz")
	if err := os.WriteFile(path, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertDemoResetRowsSurvived(t *testing.T, ctx context.Context, db *storage.DB) {
	t.Helper()
	for _, table := range []string{"parent", "child"} {
		var count int
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatalf("query %s after rollback: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("%s row count after rollback = %d, want 1", table, count)
		}
	}
}
