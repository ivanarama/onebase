package launcher

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dblock"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestOpenDBRefusesFutureRevisionBeforeSQLiteSetup(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "future.db")
	db, err := storage.ConnectSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RaiseSchemaRevision(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE _schema_revision SET revision=?, updated_by=? WHERE id=1`,
		storage.SchemaRevision+4, "future"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	raw, err := sql.Open("sqlite", filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`PRAGMA journal_mode=DELETE`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv(storage.AllowNewerSchemaEnv, "false")

	opened, err := OpenDB(ctx, &Base{ID: "future", Name: "future", DBType: "sqlite", DBPath: path})
	if opened != nil {
		opened.Close()
		t.Fatal("future database unexpectedly opened")
	}
	if !errors.Is(err, storage.ErrNewerSchema) {
		t.Fatalf("OpenDB error = %v, want ErrNewerSchema", err)
	}
	raw, err = sql.Open("sqlite", filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer closeLauncherSchemaTestResource(t, raw)
	var mode string
	if err := raw.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "delete") {
		t.Fatalf("launcher gate changed journal mode to %q, want delete", mode)
	}
}

func TestOpenDBRepairsIncompleteRevisionOnlyAfterExclusiveDrain(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "incomplete.db")
	db, err := storage.ConnectSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureSchemaRevisionSchema(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	raw, err := sql.Open("sqlite", filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`PRAGMA journal_mode=DELETE`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv(storage.AllowNewerSchemaEnv, "false")
	b := &Base{ID: "incomplete", Name: "incomplete", DBType: "sqlite", DBPath: path}

	existing, _, err := dblock.AcquireSQLiteSharedTarget(path)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenDB(ctx, b)
	if opened != nil {
		opened.Close()
	}
	if !errors.Is(err, dblock.ErrLocked) {
		_ = existing.Close()
		t.Fatalf("incomplete repair with an active consumer error = %v, want ErrLocked", err)
	}
	assertLauncherIncompleteRevisionUnchanged(t, path)
	if err := existing.Close(); err != nil {
		t.Fatal(err)
	}

	opened, err = OpenDB(ctx, b)
	if err != nil {
		t.Fatalf("repair after consumer drain: %v", err)
	}
	defer opened.Close()
	state, err := opened.SchemaRevisionStateOf(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Known || state.Revision != storage.SchemaRevision {
		t.Fatalf("repaired state = %+v, want known revision %d", state, storage.SchemaRevision)
	}
}

func assertLauncherIncompleteRevisionUnchanged(t *testing.T, path string) {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer closeLauncherSchemaTestResource(t, raw)
	var mode string
	if err := raw.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "delete") {
		t.Fatalf("failed launcher gate changed journal mode to %q, want delete", mode)
	}
	var rows int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM _schema_revision WHERE id=1`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("failed launcher gate repaired incomplete marker: rows=%d", rows)
	}
}

func TestRestoreOpenRefusesFuturePendingBeforeSQLiteSetup(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "future-pending.db")
	db, err := storage.ConnectSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.RaiseSchemaRevision(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE _schema_revision SET revision=?, updated_by=? WHERE id=1`,
		storage.SchemaRevision+4, "future"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO _settings(key,value) VALUES (?,?)`,
		"onebase.internal.restore.intent.v1", `{}`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	raw, err := sql.Open("sqlite", filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`PRAGMA journal_mode=DELETE`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	lease, target, err := dblock.AcquireSQLiteTarget(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close() //nolint:errcheck // test cleanup
	b := &Base{ID: "future-pending", Name: "future-pending", DBType: "sqlite", DBPath: target}
	ctx = context.WithValue(ctx, cfgDBExclusiveLeaseKey{}, b.ID)
	t.Setenv(storage.AllowNewerSchemaEnv, "false")

	opened, err := openDBForRestore(ctx, b)
	if opened != nil {
		opened.Close()
		t.Fatal("future pending database unexpectedly opened")
	}
	if !errors.Is(err, storage.ErrNewerSchema) {
		t.Fatalf("restore open error = %v, want ErrNewerSchema", err)
	}
	raw, err = sql.Open("sqlite", filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer closeLauncherSchemaTestResource(t, raw)
	var mode string
	if err := raw.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "delete") {
		t.Fatalf("restore gate changed journal mode to %q, want delete", mode)
	}
	var pending int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM _settings WHERE key=?`,
		"onebase.internal.restore.intent.v1").Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Fatalf("restore gate consumed future restore intent: count=%d", pending)
	}
}

func closeLauncherSchemaTestResource(t *testing.T, closer interface{ Close() error }) {
	t.Helper()
	if err := closer.Close(); err != nil {
		t.Errorf("close launcher schema-gate test resource: %v", err)
	}
}
