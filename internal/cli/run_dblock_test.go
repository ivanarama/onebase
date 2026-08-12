package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/dblock"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestServerInitialLeaseCoexistsWithOrdinarySQLiteConsumer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "base.db")
	consumer, err := dblock.AcquireSQLiteShared(path)
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close() //nolint:errcheck // test cleanup

	server, canonical, err := acquireServerDatabaseLease(t.Context(), "sqlite", path, "", true)
	if err != nil {
		t.Fatalf("normal server startup contended with ordinary consumer: %v", err)
	}
	defer server.Close() //nolint:errcheck // test cleanup
	if canonical == "" {
		t.Fatal("server startup did not pin the canonical SQLite target")
	}

	restore, err := dblock.AcquireSQLite(path)
	if restore != nil {
		_ = restore.Close()
	}
	if !errors.Is(err, dblock.ErrLocked) {
		t.Fatalf("destructive restore while server consumers are active = %v, want ErrLocked", err)
	}
}

func TestPerformScheduledDemoResetSQLiteIsOfflineAndUsesPinnedFilesDir(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()

	sourcePath := filepath.Join(root, "source.db")
	source, err := storage.ConnectSQLite(ctx, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(ctx, `CREATE TABLE items (id TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		source.Close()
		t.Fatal(err)
	}
	if _, err := source.Exec(ctx, `INSERT INTO items(id, value) VALUES ('wanted', 'fresh')`); err != nil {
		source.Close()
		t.Fatal(err)
	}
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		source.Close()
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "onebase.yaml"), []byte("name: demo\n"), 0o600); err != nil {
		source.Close()
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := backup.ExportUniversal(ctx, source, "file", configDir, "", "demo", &archive); err != nil {
		source.Close()
		t.Fatal(err)
	}
	source.Close()
	archivePath := filepath.Join(root, "demo.obz")
	if err := os.WriteFile(archivePath, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	targetPath := filepath.Join(root, "target.db")
	target, err := storage.ConnectSQLite(ctx, targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Exec(ctx, `CREATE TABLE items (id TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		target.Close()
		t.Fatal(err)
	}
	if _, err := target.Exec(ctx, `INSERT INTO items(id, value) VALUES ('stale', 'old')`); err != nil {
		target.Close()
		t.Fatal(err)
	}
	if err := configdb.New(target).EnsureSchema(ctx); err != nil {
		target.Close()
		t.Fatal(err)
	}
	if err := target.EnsureScheduledRunsTable(ctx); err != nil {
		target.Close()
		t.Fatal(err)
	}
	runStarted := time.Now()
	runID, err := target.InsertScheduledRun(ctx, "DemoReset", runStarted)
	if err != nil {
		target.Close()
		t.Fatal(err)
	}
	if err := target.UpdateScheduledRun(ctx, runID, "accepted", "offline demo reset request accepted", "", 0); err != nil {
		target.Close()
		t.Fatal(err)
	}
	target.Close()

	filesDir := filepath.Join(root, "pinned-files")
	if err := os.MkdirAll(filesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	staleAttachment := filepath.Join(filesDir, "stale")
	if err := os.WriteFile(staleAttachment, []byte("must disappear"), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := performScheduledDemoReset(ctx, &scheduledDemoResetRequest{
		dbType:     "sqlite",
		sqlitePath: targetPath,
		filesDir:   filesDir,
		backupPath: archivePath,
		run:        scheduler.RunInfo{ID: runID, StartedAt: runStarted},
	})
	if err != nil {
		t.Fatalf("performScheduledDemoReset: %v", err)
	}
	if report.Tables["items"] != 1 {
		t.Fatalf("restored items = %d, want 1", report.Tables["items"])
	}
	if _, err := os.Stat(staleAttachment); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale file in pinned files dir survived reset: %v", err)
	}

	check, err := storage.ConnectSQLite(ctx, targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var id, value string
	if err := check.QueryRow(ctx, `SELECT id, value FROM items`).Scan(&id, &value); err != nil {
		t.Fatal(err)
	}
	if id != "wanted" || value != "fresh" {
		t.Fatalf("restored row = (%q, %q), want (wanted, fresh)", id, value)
	}
	runs, err := check.ScheduledRuns(ctx, "DemoReset", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != "success" || runs[0].FinishedAt == nil {
		t.Fatalf("offline reset result was not published: %+v", runs)
	}
}

func TestPerformScheduledDemoResetRefusesLiveSQLiteConsumer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "base.db")
	db, err := storage.ConnectSQLite(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureScheduledRunsTable(t.Context()); err != nil {
		db.Close()
		t.Fatal(err)
	}
	startedAt := time.Now()
	runID, err := db.InsertScheduledRun(t.Context(), "DemoReset", startedAt)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.UpdateScheduledRun(t.Context(), runID, "accepted", "offline demo reset request accepted", "", 0); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	consumer, err := dblock.AcquireSQLiteShared(path)
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close() //nolint:errcheck // test cleanup

	_, err = performScheduledDemoReset(t.Context(), &scheduledDemoResetRequest{
		dbType:     "sqlite",
		sqlitePath: path,
		filesDir:   filepath.Join(t.TempDir(), "files"),
		backupPath: filepath.Join(t.TempDir(), "missing.obz"),
		run:        scheduler.RunInfo{ID: runID, StartedAt: startedAt},
	})
	if !errors.Is(err, dblock.ErrLocked) {
		t.Fatalf("reset with live consumer error = %v, want ErrLocked", err)
	}
	if err := consumer.Close(); err != nil {
		t.Fatal(err)
	}
	check, openErr := storage.ConnectSQLite(t.Context(), path)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer check.Close()
	runs, queryErr := check.ScheduledRuns(t.Context(), "DemoReset", 1)
	if queryErr != nil {
		t.Fatal(queryErr)
	}
	if len(runs) != 1 || runs[0].Status != "error" || runs[0].FinishedAt == nil || runs[0].Error == "" {
		t.Fatalf("rejected offline reset result was not published: %+v", runs)
	}
}
