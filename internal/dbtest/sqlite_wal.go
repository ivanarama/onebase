package dbtest

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// SQLiteHotWALValue is committed by CreateSQLiteHotWALCrash but remains only
// in the WAL until the next writable SQLite opener performs recovery.
const SQLiteHotWALValue = "committed-only-in-hot-wal"

const sqliteHotWALChildEnv = "ONEBASE_DBTEST_SQLITE_HOT_WAL_CHILD"

// CreateSQLiteHotWALCrash leaves path in the same state as an SQLite database
// after an abrupt process stop: the main file is valid, while the latest
// committed transaction exists only in a hot WAL. A subprocess is required;
// closing database/sql normally would checkpoint the WAL and erase the state
// that startup code must recover.
func CreateSQLiteHotWALCrash(t *testing.T, path string) {
	t.Helper()

	if childPath := os.Getenv(sqliteHotWALChildEnv); childPath != "" {
		if err := writeSQLiteHotWAL(childPath); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}

	command := exec.Command(os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$") //nolint:gosec // G204: повторно запускается текущий тестовый бинарь без shell; имя теста экранировано и зажато якорями
	command.Env = append(os.Environ(), sqliteHotWALChildEnv+"="+path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create SQLite hot-WAL crash image: %v\n%s", err, output)
	}
	info, err := os.Stat(path + "-wal")
	if err != nil {
		t.Fatalf("stat SQLite hot WAL: %v", err)
	}
	if info.Size() <= 32 {
		t.Fatalf("SQLite WAL is not hot: size=%d", info.Size())
	}
}

func writeSQLiteHotWAL(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { //nolint:gosec // G703: путь передан родительским тестом из его t.TempDir через служебную переменную окружения
		return fmt.Errorf("create hot-WAL directory: %w", err)
	}
	db, err := sql.Open("sqlite", filepath.ToSlash(path))
	if err != nil {
		return fmt.Errorf("open hot-WAL database: %w", err)
	}
	db.SetMaxOpenConns(1)

	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&mode); err != nil {
		return fmt.Errorf("enable WAL: %w", err)
	}
	if mode != "wal" {
		return fmt.Errorf("enable WAL: journal mode is %q", mode)
	}
	for _, statement := range []string{
		"PRAGMA synchronous=FULL",
		"PRAGMA wal_autocheckpoint=0",
		"CREATE TABLE hot_wal_values (value TEXT NOT NULL)",
		"PRAGMA wal_checkpoint(TRUNCATE)",
	} {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("prepare hot WAL with %q: %w", statement, err)
		}
	}
	if _, err := db.Exec("INSERT INTO hot_wal_values(value) VALUES (?)", SQLiteHotWALValue); err != nil {
		return fmt.Errorf("commit value to hot WAL: %w", err)
	}
	info, err := os.Stat(path + "-wal") //nolint:gosec // G703: тот же принадлежащий тесту временный путь; суффикс фиксирован
	if err != nil {
		return fmt.Errorf("stat child WAL: %w", err)
	}
	if info.Size() <= 32 {
		return fmt.Errorf("child WAL is not hot: size=%d", info.Size())
	}

	// Intentionally do not close db. The caller exits the subprocess
	// immediately, modelling a killed server without a last-close checkpoint.
	return nil
}
