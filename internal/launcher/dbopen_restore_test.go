package launcher

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/storage"
)

const testRestoreIntentKey = "onebase.internal.restore.intent.v1"

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
