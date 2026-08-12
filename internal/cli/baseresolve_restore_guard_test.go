package cli

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/storage"
)

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
