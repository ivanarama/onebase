//go:build integration

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

func TestDemoResetPostgresRollbackKeepsPrimaryErrorAndForeignKeys(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	filesParent := t.TempDir()
	filesDir := filepath.Join(filesParent, "files")
	t.Setenv("ONEBASE_FILES_DIR", filesDir)

	schema := storage.NewEphemeralSchemaName()
	db, err := storage.ConnectWithSchema(ctx, dsn, schema)
	if err != nil {
		t.Fatalf("ConnectWithSchema: %v", err)
	}
	if err := db.CreateSchema(ctx, schema); err != nil {
		db.Close()
		t.Fatalf("CreateSchema: %v", err)
	}
	t.Cleanup(func() {
		if err := db.DropSchemaCascade(context.Background(), schema); err != nil {
			t.Errorf("DropSchemaCascade(%s): %v", schema, err)
		}
		db.Close()
	})
	db.SetFilesDir(filesDir)
	if err := os.MkdirAll(filesDir, 0o750); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(filesDir, "sentinel.bin")
	if err := os.WriteFile(sentinel, []byte("original attachment"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(ctx, `CREATE TABLE parent (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `CREATE TABLE child (
		id TEXT PRIMARY KEY,
		parent_id TEXT NOT NULL,
		CONSTRAINT demo_reset_child_parent_fk FOREIGN KEY (parent_id) REFERENCES parent(id)
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

	_, err = DemoReset(ctx, db, writeFailingDemoResetArchive(t))
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("DemoReset error = %v, want primary data import error", err)
	}
	if strings.Contains(err.Error(), "tx is closed") || strings.Contains(err.Error(), "restore FK") {
		t.Fatalf("DemoReset masked the primary error with post-rollback FK cleanup: %v", err)
	}

	assertDemoResetRowsSurvived(t, ctx, db)
	var exists, validated bool
	if err := db.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pg_constraint
		WHERE connamespace=current_schema()::regnamespace
		  AND conname='demo_reset_child_parent_fk'
		  AND contype='f'
		  AND convalidated
	), COALESCE((
		SELECT convalidated FROM pg_constraint
		WHERE connamespace=current_schema()::regnamespace
		  AND conname='demo_reset_child_parent_fk'
		  AND contype='f'
	), false)`).Scan(&exists, &validated); err != nil {
		t.Fatalf("inspect FK after rollback: %v", err)
	}
	if !exists || !validated {
		t.Fatalf("FK after rollback: exists=%v validated=%v, want true/true", exists, validated)
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "original attachment" {
		t.Fatalf("sentinel after rollback = %q, %v", got, err)
	}
	entries, err := os.ReadDir(filesParent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".restore-stage-") ||
			strings.Contains(entry.Name(), ".restore-old-") ||
			strings.Contains(entry.Name(), ".restore-retired-") {
			t.Fatalf("restore temporary tree leaked after rollback: %s", entry.Name())
		}
	}
}
