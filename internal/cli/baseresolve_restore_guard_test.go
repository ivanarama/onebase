package cli

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestOpenCLIStorageRecoversHotWALAfterUncleanShutdown(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "hot-wal.db")
	dbtest.CreateSQLiteHotWALCrash(t, dbPath)

	db, err := openCLIStorage(ctx, "sqlite", dbPath, "")
	if err != nil {
		t.Fatalf("openCLIStorage with hot WAL: %v", err)
	}
	defer db.Close()

	var got string
	if err := db.QueryRow(ctx, `SELECT value FROM hot_wal_values`).Scan(&got); err != nil {
		t.Fatalf("read transaction recovered from hot WAL: %v", err)
	}
	if got != dbtest.SQLiteHotWALValue {
		t.Fatalf("recovered value = %q, want %q", got, dbtest.SQLiteHotWALValue)
	}
}

func TestOpenCLIStorageRejectsPendingRestore(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "pending.db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO _settings (key,value) VALUES (?,?)`,
		"onebase.internal.restore.intent.v1",
		`{"version":1,"id":"test","state":"pending","swaps":[{"dest":"ignored","stage":"ignored","backup":"ignored"}]}`,
	); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	opened, err := openCLIStorage(ctx, "sqlite", dbPath, "")
	if opened != nil {
		opened.Close()
		t.Fatal("ordinary CLI open returned a database with pending restore")
	}
	if !errors.Is(err, backup.ErrRestoreRecoveryRequired) {
		t.Fatalf("open error = %v, want ErrRestoreRecoveryRequired", err)
	}
}
