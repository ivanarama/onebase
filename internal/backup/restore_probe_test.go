package backup

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
)

func openRawSQLiteForProbeTest(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestHasPendingRestoreSQLiteDoesNotCreateMissingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	pending, err := HasPendingRestoreSQLite(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("missing database unexpectedly has a restore marker")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("startup probe created the database: %v", err)
	}
}

func TestHasPendingRestoreSQLiteFindsMarkerWithoutChangingJournalMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "base # marker.db")
	db := openRawSQLiteForProbeTest(t, path)
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE _settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO _settings(key,value) VALUES (?,?)`, restoreIntentKey, `{}`); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := db.QueryRowContext(t.Context(), `PRAGMA journal_mode`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	pending, err := HasPendingRestoreSQLite(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !pending {
		t.Fatal("startup probe missed the durable restore marker")
	}

	readOnly, err := sql.Open("sqlite", sqliteFileURI(path, "mode=ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close() //nolint:errcheck // test cleanup
	var after string
	if err := readOnly.QueryRowContext(t.Context(), `PRAGMA journal_mode`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("restore probe changed journal mode from %q to %q", before, after)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), ".onebase_probe")); !os.IsNotExist(err) {
		t.Fatalf("restore probe left a write-permission file: %v", err)
	}
}

func TestHasPendingRestoreSQLiteSeesCommittedWALMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.db")
	db := openRawSQLiteForProbeTest(t, path)
	var mode string
	if err := db.QueryRowContext(t.Context(), `PRAGMA journal_mode=WAL`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Skipf("WAL unavailable: %s", mode)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE _settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO _settings(key,value) VALUES (?,?)`, restoreIntentKey, `{}`); err != nil {
		t.Fatal(err)
	}

	pending, err := HasPendingRestoreSQLite(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !pending {
		t.Fatal("startup probe ignored a committed marker in the WAL")
	}
}

func TestHasPendingRestoreSQLiteRecoversHotWALForFollowingReadOnlyGates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hot-wal.db")
	dbtest.CreateSQLiteHotWALCrash(t, path)

	pending, err := HasPendingRestoreSQLite(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("hot WAL without a restore marker reported pending recovery")
	}

	// immutable=1 deliberately ignores WAL. Seeing the committed value proves
	// that the startup probe checkpointed crash recovery into the main file,
	// so the following read-only schema-revision gate no longer hits a hot WAL.
	mainOnly, err := sql.Open("sqlite", sqliteFileURI(path, "mode=ro&immutable=1"))
	if err != nil {
		t.Fatal(err)
	}
	defer mainOnly.Close() //nolint:errcheck // test cleanup
	var got string
	if err := mainOnly.QueryRowContext(t.Context(), `SELECT value FROM hot_wal_values`).Scan(&got); err != nil {
		t.Fatalf("read recovered value from main database: %v", err)
	}
	if got != dbtest.SQLiteHotWALValue {
		t.Fatalf("recovered value = %q, want %q", got, dbtest.SQLiteHotWALValue)
	}
}

func TestHasPendingRestoreSQLiteMalformedSettingsFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "malformed.db")
	db := openRawSQLiteForProbeTest(t, path)
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE _settings (other TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if pending, err := HasPendingRestoreSQLite(t.Context(), path); err == nil {
		t.Fatalf("malformed settings probe = %v, nil; want fail-closed error", pending)
	} else if message := err.Error(); !strings.Contains(message, "SQLite startup safety check did not complete") ||
		!strings.Contains(message, "keep the database, -wal and -shm files together") ||
		!strings.Contains(message, "do not delete -wal by hand") {
		t.Fatalf("probe error does not explain the failed safety check: %v", err)
	} else if strings.Contains(message, "SQLite startup recovery failed") {
		t.Fatalf("schema error incorrectly claims that WAL recovery failed: %v", err)
	}
}
