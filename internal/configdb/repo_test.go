//go:build integration

package configdb_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/storage"
)

func connectTestDB(t *testing.T) *storage.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := storage.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func TestRepo_EnsureSchema(t *testing.T) {
	db := connectTestDB(t)
	ctx := context.Background()
	repo := configdb.New(db.Pool())

	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	// Idempotent
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema (second call): %v", err)
	}
}

func TestRepo_IsEmpty(t *testing.T) {
	db := connectTestDB(t)
	ctx := context.Background()
	repo := configdb.New(db.Pool())
	repo.EnsureSchema(ctx)
	db.Pool().Exec(ctx, `DELETE FROM _onebase_config`)

	empty, err := repo.IsEmpty(ctx)
	if err != nil {
		t.Fatalf("IsEmpty: %v", err)
	}
	if !empty {
		t.Fatal("should be empty after DELETE")
	}
}

func TestRepo_ImportExportRoundTrip(t *testing.T) {
	db := connectTestDB(t)
	ctx := context.Background()
	repo := configdb.New(db.Pool())
	repo.EnsureSchema(ctx)
	db.Pool().Exec(ctx, `DELETE FROM _onebase_config`)

	// Build a small project directory
	srcDir := t.TempDir()
	subDir := filepath.Join(srcDir, "catalogs")
	os.MkdirAll(subDir, 0o755)
	os.WriteFile(filepath.Join(srcDir, "config", "app.yaml"), []byte("name: test\n"), 0o644)
	os.MkdirAll(filepath.Join(srcDir, "config"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "config", "app.yaml"), []byte("name: test\nversion: \"1.0\"\n"), 0o644)
	os.WriteFile(filepath.Join(subDir, "item.yaml"), []byte("name: Item\nfields:\n  - name: Name\n    type: string\n"), 0o644)

	if err := repo.ImportFromDir(ctx, srcDir); err != nil {
		t.Fatalf("ImportFromDir: %v", err)
	}

	empty, _ := repo.IsEmpty(ctx)
	if empty {
		t.Fatal("should not be empty after import")
	}

	// Export to a new dir and compare
	dstDir := t.TempDir()
	if err := repo.ExportToDir(ctx, dstDir); err != nil {
		t.Fatalf("ExportToDir: %v", err)
	}

	// Check that exported files match originals
	checkFile := func(rel string) {
		t.Helper()
		src, err := os.ReadFile(filepath.Join(srcDir, rel))
		if err != nil {
			t.Fatalf("read src %s: %v", rel, err)
		}
		dst, err := os.ReadFile(filepath.Join(dstDir, rel))
		if err != nil {
			t.Fatalf("read dst %s: %v", rel, err)
		}
		if string(src) != string(dst) {
			t.Fatalf("file %s mismatch:\nsrc: %q\ndst: %q", rel, src, dst)
		}
	}
	checkFile("config/app.yaml")
	checkFile("catalogs/item.yaml")
}

func TestRepo_ImportReplacesPrevious(t *testing.T) {
	db := connectTestDB(t)
	ctx := context.Background()
	repo := configdb.New(db.Pool())
	repo.EnsureSchema(ctx)
	db.Pool().Exec(ctx, `DELETE FROM _onebase_config`)

	// First import
	dir1 := t.TempDir()
	os.WriteFile(filepath.Join(dir1, "file1.txt"), []byte("v1"), 0o644)
	repo.ImportFromDir(ctx, dir1)

	// Second import — replaces all
	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "file2.txt"), []byte("v2"), 0o644)
	repo.ImportFromDir(ctx, dir2)

	dst := t.TempDir()
	repo.ExportToDir(ctx, dst)

	// file1.txt should not exist
	if _, err := os.Stat(filepath.Join(dst, "file1.txt")); !os.IsNotExist(err) {
		t.Fatal("file1.txt should have been replaced")
	}
	// file2.txt should exist
	if _, err := os.Stat(filepath.Join(dst, "file2.txt")); err != nil {
		t.Fatal("file2.txt should exist")
	}
}
