package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// HasPendingRestoreSQLite inspects the durable restore marker without opening
// OneBase storage. It never creates the database, changes its journal mode,
// runs application writes, or applies connection setup pragmas. The existing
// file is opened read/write intentionally: after an abrupt stop SQLite must be
// allowed to recover and checkpoint a hot WAL before the following independent
// read-only schema-revision probe. The caller must hold the database lifetime
// lease so the result stays valid until it either opens as a consumer or
// switches to the exclusive recovery path.
func HasPendingRestoreSQLite(ctx context.Context, path string) (pending bool, resultErr error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return false, errors.New("restore probe: empty SQLite path")
	}
	if isMemorySQLitePath(path) {
		// An in-memory database cannot contain a journal left by an earlier
		// process. Avoid creating a throwaway database merely to prove that.
		return false, nil
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("restore probe: stat SQLite database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("restore probe: SQLite target is not a regular file")
	}

	db, err := sql.Open("sqlite", sqliteFileURI(path, "mode=rw"))
	if err != nil {
		return false, sqliteRestoreProbeError("open existing SQLite database for WAL recovery", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer func() {
		resultErr = errors.Join(resultErr, db.Close())
	}()

	var tableExists bool
	if err := db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='_settings')`,
	).Scan(&tableExists); err != nil {
		return false, sqliteRestoreProbeError("inspect SQLite settings table", err)
	}
	if !tableExists {
		return false, nil
	}
	if err := db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM _settings WHERE key=?)`, restoreIntentKey,
	).Scan(&pending); err != nil {
		return false, sqliteRestoreProbeError("inspect SQLite marker", err)
	}
	return pending, nil
}

func sqliteRestoreProbeError(action string, err error) error {
	return fmt.Errorf("restore probe: %s: %w\n"+
		"SQLite startup safety check did not complete: keep the database, -wal and -shm files together, "+
		"stop other OneBase processes, ensure write access, and retry; do not delete -wal by hand",
		action, err)
}

// HasPendingRestorePostgres uses one plain pgx connection and read-only SELECTs
// rather than storage.Connect, whose normal compatibility setup performs DDL.
func HasPendingRestorePostgres(ctx context.Context, dsn string) (pending bool, resultErr error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return false, fmt.Errorf("restore probe: parse PostgreSQL DSN: %w", err)
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return false, fmt.Errorf("restore probe: connect PostgreSQL read-only: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		resultErr = errors.Join(resultErr, conn.Close(closeCtx))
	}()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return false, fmt.Errorf("restore probe: begin PostgreSQL read-only transaction: %w", err)
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		resultErr = errors.Join(resultErr, tx.Rollback(rollbackCtx))
	}()

	var tableExists bool
	if err := tx.QueryRow(ctx, `SELECT pg_catalog.to_regclass('_settings') IS NOT NULL`).Scan(&tableExists); err != nil {
		return false, fmt.Errorf("restore probe: inspect PostgreSQL settings table: %w", err)
	}
	if !tableExists {
		return false, nil
	}
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM _settings WHERE key=$1)`, restoreIntentKey,
	).Scan(&pending); err != nil {
		return false, fmt.Errorf("restore probe: inspect PostgreSQL marker: %w", err)
	}
	return pending, nil
}

func isMemorySQLitePath(path string) bool {
	if path == ":memory:" {
		return true
	}
	low := strings.ToLower(path)
	return strings.HasPrefix(low, "file::memory:") ||
		(strings.HasPrefix(low, "file:") && strings.Contains(low, "mode=memory"))
}
