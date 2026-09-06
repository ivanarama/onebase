package launcher

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

const testRestoreIntentKey = "onebase.internal.restore.intent.v1"

func TestOpenDBRecoversHotWALAfterUncleanShutdown(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "hot-wal.db")
	dbtest.CreateSQLiteHotWALCrash(t, dbPath)
	b := &Base{ID: "hot-wal-open", Name: "hot-wal-open", DBType: "sqlite", DBPath: dbPath}

	db, err := OpenDB(ctx, b)
	if err != nil {
		t.Fatalf("OpenDB with hot WAL: %v", err)
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

func TestOpenDBFailsClosedWithPendingRestore(t *testing.T) {
	ctx := context.Background()
	b := &Base{ID: "pending-open", Name: "pending-open", DBType: "sqlite", DBPath: filepath.Join(t.TempDir(), "base.db")}

	db, err := openDBUnchecked(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO _settings(key,value) VALUES (?,?)`, testRestoreIntentKey, `{}`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	opened, err := OpenDB(ctx, b)
	if opened != nil {
		opened.Close()
		t.Fatal("OpenDB returned a database with a durable restore marker")
	}
	if !errors.Is(err, backup.ErrRestoreRecoveryRequired) {
		t.Fatalf("OpenDB error = %v, want ErrRestoreRecoveryRequired", err)
	}
}

func TestOpenDBForRestoreRequiresExclusiveLease(t *testing.T) {
	ctx := context.Background()
	b := &Base{ID: "restore-open", Name: "restore-open", DBType: "sqlite", DBPath: filepath.Join(t.TempDir(), "base.db")}
	if db, err := openDBForRestore(ctx, b); db != nil || err == nil {
		if db != nil {
			db.Close()
		}
		t.Fatalf("openDBForRestore without lease = (%v, %v), want rejection", db, err)
	}
	ctx = context.WithValue(ctx, cfgDBExclusiveLeaseKey{}, b.ID)
	db, err := openDBForRestore(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
}

func TestGetAuthDBRechecksCachedPoolForPendingRestore(t *testing.T) {
	ctx := context.Background()
	b := &Base{ID: "pending-cached-auth", Name: "pending-cached-auth", DBType: "sqlite", DBPath: filepath.Join(t.TempDir(), "base.db")}
	t.Cleanup(func() {
		if value, ok := cfgAuthDBs.LoadAndDelete(b.ID); ok {
			value.(*storage.DB).Close()
		}
	})

	db, err := getAuthDB(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO _settings(key,value) VALUES (?,?)`, testRestoreIntentKey, `{}`); err != nil {
		t.Fatal(err)
	}

	if got, err := getAuthDB(ctx, b); got != nil || !errors.Is(err, backup.ErrRestoreRecoveryRequired) {
		t.Fatalf("getAuthDB = (%v, %v), want nil ErrRestoreRecoveryRequired", got, err)
	}
}
