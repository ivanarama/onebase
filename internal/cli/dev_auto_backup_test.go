package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/storage"
)

func TestDevAutoBackupTargetUsesOpenedCanonicalSQLiteFile(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasDir := filepath.Join(root, "alias")
	if err := os.Symlink(realDir, aliasDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	aliasPath := filepath.Join(aliasDir, "dev.db")
	db, err := openCLIStorage(context.Background(), "sqlite", aliasPath, "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	target, err := devAutoBackupTarget(db, "sqlite", "ignored", root)
	if err != nil {
		t.Fatal(err)
	}
	if target.SQLitePath != db.SQLitePath() {
		t.Fatalf("backup path = %q, opened database = %q", target.SQLitePath, db.SQLitePath())
	}
	want := filepath.Join(realDir, "dev.db")
	if target.SQLitePath != want {
		t.Fatalf("backup path = %q, want canonical symlink target %q", target.SQLitePath, want)
	}
	if target.SQLitePath == aliasPath {
		t.Fatalf("backup retained re-resolvable symlink spelling %q", aliasPath)
	}
}

func TestDevAutoBackupTargetRejectsInMemorySQLite(t *testing.T) {
	db, err := storage.ConnectSQLite(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = devAutoBackupTarget(db, "sqlite", "", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "file-backed SQLite") {
		t.Fatalf("in-memory auto-backup error = %v", err)
	}
}

func TestDevAutoBackupTargetKeepsPostgresDSN(t *testing.T) {
	target, err := devAutoBackupTarget(nil, "postgres", "postgres://db/onebase", "project")
	if err != nil {
		t.Fatal(err)
	}
	if target.DSN != "postgres://db/onebase" || target.SQLitePath != "" || target.ProjectDir != "project" {
		t.Fatalf("unexpected PostgreSQL target: %+v", target)
	}
}
