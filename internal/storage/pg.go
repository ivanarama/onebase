package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB is the database handle abstraction. It carries either a PostgreSQL
// pgxpool.Pool or a SQLite *sql.DB, plus the matching Dialect. All Exec/Query
// methods route to the right backend transparently.
type DB struct {
	pool     *pgxpool.Pool // non-nil for PG
	sqlDB    *sql.DB       // non-nil for SQLite
	filesDir string
	dialect  Dialect
}

func pgQuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func Connect(ctx context.Context, dsn string) (*DB, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("storage: ping: %w", err)
	}
	// Create implicit uuid→text cast so that SQL queries written for SQLite
	// (where uuid is stored as text) work on PostgreSQL without explicit
	// casts.  WITH INOUT uses type I/O functions (uuidout→textin) directly,
	// avoiding recursion that a custom function with ::text would cause.
	_, _ = pool.Exec(ctx, `DROP CAST IF EXISTS (uuid AS text)`)
	_, _ = pool.Exec(ctx, `CREATE CAST (uuid AS text) WITH INOUT AS IMPLICIT`)

	filesDir := defaultFilesDir(dsn)
	return &DB{pool: pool, filesDir: filesDir, dialect: PgDialect{}}, nil
}

// Dialect returns the SQL dialect for this connection. Use it to build SQL
// that runs identically on PostgreSQL and SQLite.
func (db *DB) Dialect() Dialect { return db.dialect }

// IsSQLite reports whether this is a SQLite-backed connection.
func (db *DB) IsSQLite() bool { return db.sqlDB != nil }

// IsPostgres reports whether this is a PostgreSQL-backed connection.
func (db *DB) IsPostgres() bool { return db.pool != nil }

// Ping проверяет доступность БД — для readiness-проб (/healthz). Работает и для
// PostgreSQL (пул), и для SQLite (database/sql).
func (db *DB) Ping(ctx context.Context) error {
	switch {
	case db.pool != nil:
		return db.pool.Ping(ctx)
	case db.sqlDB != nil:
		return db.sqlDB.PingContext(ctx)
	default:
		return fmt.Errorf("storage: no database connection")
	}
}

func defaultFilesDir(dsn string) string {
	cfg, err := pgxpool.ParseConfig(dsn)
	dbName := "default"
	if err == nil && cfg.ConnConfig.Database != "" {
		dbName = cfg.ConnConfig.Database
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".onebase", "files", dbName)
}

func (db *DB) FilesDir() string { return db.filesDir }

// SetFilesDir переопределяет каталог файлового хранилища (вложения, блобы).
func (db *DB) SetFilesDir(dir string) { db.filesDir = dir }

func (db *DB) Close() {
	webhookURLScrubbed.Delete(db)
	if db.pool != nil {
		db.pool.Close()
	}
	if db.sqlDB != nil {
		_ = db.sqlDB.Close()
	}
}

// DisableFKForImport disables foreign-key constraint enforcement for the
// duration of a bulk import and returns a cleanup function that re-enables it.
//
// SQLite: pins the connection pool to 1 connection so that the PRAGMA applies
// to every subsequent statement, then executes PRAGMA foreign_keys=OFF.
// The cleanup restores PRAGMA foreign_keys=ON and the pool size.
//
// PostgreSQL: drops all FK constraints via ALTER TABLE (DDL), which reliably
// affects every connection in the pool. Previously we used
// SET session_replication_role='replica', but that is a session-level setting
// that only applies to ONE connection — other pool connections still enforce
// FK constraints (including ON DELETE CASCADE), causing silent data loss
// when import reorders tables and intermittent FK violation errors.
func (db *DB) DisableFKForImport(ctx context.Context) (cleanup func() error, err error) {
	if db.sqlDB != nil {
		db.sqlDB.SetMaxOpenConns(1)
		if _, err := db.sqlDB.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
			db.sqlDB.SetMaxOpenConns(0)
			return func() error { return nil }, err
		}
		return func() error {
			_, err := db.sqlDB.ExecContext(context.Background(), "PRAGMA foreign_keys=ON")
			db.sqlDB.SetMaxOpenConns(0)
			return err
		}, nil
	}
	// PostgreSQL: drop all FK constraints (DDL affects all pool connections).
	type fkInfo struct {
		table string
		name  string
		def   string
	}
	// Use pg_class.relname (always unquoted) instead of regclass::text
	// which may return quoted identifiers like "возвратотпокупателя",
	// causing double-quoting and silent ALTER TABLE failures.
	rows, err := db.pool.Query(ctx,
		`SELECT c.conname, t.relname, pg_get_constraintdef(c.oid)
		 FROM pg_constraint c
		 JOIN pg_class t ON c.conrelid = t.oid
		 WHERE c.contype='f' AND c.connamespace='public'::regnamespace`)
	if err != nil {
		return func() error { return nil }, err
	}
	var fks []fkInfo
	for rows.Next() {
		var name, table, def string
		if err := rows.Scan(&name, &table, &def); err != nil {
			rows.Close()
			return func() error { return nil }, err
		}
		fks = append(fks, fkInfo{table: table, name: name, def: def})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return func() error { return nil }, err
	}

	restore := func(items []fkInfo) error {
		var errs []error
		for _, fk := range items {
			tq := `"` + strings.ReplaceAll(fk.table, `"`, `""`) + `"`
			nq := `"` + strings.ReplaceAll(fk.name, `"`, `""`) + `"`
			if _, err := db.pool.Exec(context.Background(),
				"ALTER TABLE "+tq+" ADD CONSTRAINT "+nq+" "+fk.def+" NOT VALID"); err != nil {
				errs = append(errs, fmt.Errorf("restore FK %s.%s: %w", fk.table, fk.name, err))
			}
		}
		return errors.Join(errs...)
	}
	var dropped []fkInfo
	for _, fk := range fks {
		tq := `"` + strings.ReplaceAll(fk.table, `"`, `""`) + `"`
		nq := `"` + strings.ReplaceAll(fk.name, `"`, `""`) + `"`
		if _, err := db.pool.Exec(ctx, "ALTER TABLE "+tq+" DROP CONSTRAINT "+nq); err != nil {
			return func() error { return nil }, errors.Join(
				fmt.Errorf("drop FK %s.%s: %w", fk.table, fk.name, err), restore(dropped))
		}
		dropped = append(dropped, fk)
	}
	return func() error { return restore(dropped) }, nil
}

// EnsureDatabase creates the PostgreSQL database named in dsn if it does not
// exist. It connects via the "postgres" maintenance database to issue
// CREATE DATABASE, so the caller doesn't need to create the DB manually.
func EnsureDatabase(ctx context.Context, dsn string) error {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("storage: parse dsn: %w", err)
	}
	dbName := cfg.ConnConfig.Database
	if dbName == "" || dbName == "postgres" {
		return nil // nothing to create
	}

	// Connect to the maintenance database
	cfg.ConnConfig.Database = "postgres"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("storage: connect to postgres db: %w", err)
	}
	defer pool.Close()

	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, dbName,
	).Scan(&exists); err != nil {
		return fmt.Errorf("storage: check db existence: %w", err)
	}
	if exists {
		return nil
	}

	safe := strings.ReplaceAll(dbName, `"`, `""`)
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE DATABASE "%s"`, safe)); err != nil {
		return fmt.Errorf("storage: create database %q: %w", dbName, err)
	}
	return nil
}
