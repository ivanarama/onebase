package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// IsInMemorySQLitePath reports targets that cannot be inspected or prepared by
// a separate connection because every such connection owns a distinct in-memory
// database. High-level openers prepare their marker on the returned DB instead.
func IsInMemorySQLitePath(path string) bool { return isInMemorySQLite(path) }

// ProbeSQLiteSchemaRevision reads the compatibility marker without normal
// OneBase connection setup. In particular it does not switch journal mode or
// apply operational pragmas. The caller must hold the database lifetime lease
// so the result remains valid until it either opens the database or upgrades
// the marker under an exclusive lease.
func ProbeSQLiteSchemaRevision(ctx context.Context, path string) (state SchemaRevisionState, resultErr error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return state, errors.New("storage: schema revision probe: empty SQLite path")
	}
	if isInMemorySQLite(path) {
		return state, nil
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("storage: schema revision probe: stat SQLite database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return state, errors.New("storage: schema revision probe: SQLite target is not a regular file")
	}

	db, err := sql.Open("sqlite", schemaRevisionSQLiteURI(path, "mode=ro"))
	if err != nil {
		return state, fmt.Errorf("storage: schema revision probe: open SQLite read-only: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer func() { resultErr = errors.Join(resultErr, db.Close()) }()

	if err := db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='_schema_revision')`,
	).Scan(&state.TableExists); err != nil {
		return state, fmt.Errorf("storage: schema revision probe: inspect SQLite marker table: %w", err)
	}
	if !state.TableExists {
		return state, nil
	}
	err = db.QueryRowContext(ctx,
		`SELECT revision, updated_by FROM _schema_revision WHERE id = 1`,
	).Scan(&state.Revision, &state.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("storage: schema revision probe: read SQLite marker: %w", err)
	}
	state.Known = true
	return state, nil
}

// PrepareSQLiteSchemaRevision atomically creates/raises the compatibility
// marker without applying normal SQLite connection pragmas. It is called only
// while the caller owns the exclusive database lifetime lease.
func PrepareSQLiteSchemaRevision(ctx context.Context, path string) (state SchemaRevisionState, resultErr error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return state, errors.New("storage: schema revision prepare: empty SQLite path")
	}
	if isInMemorySQLite(path) {
		return state, errors.New("storage: schema revision prepare: in-memory SQLite requires the opened connection")
	}

	db, err := sql.Open("sqlite", schemaRevisionSQLiteURI(path, "mode=rwc"))
	if err != nil {
		return state, fmt.Errorf("storage: schema revision prepare: open SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer func() { resultErr = errors.Join(resultErr, db.Close()) }()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return state, fmt.Errorf("storage: schema revision prepare: begin SQLite transaction: %w", err)
	}
	open := true
	defer func() {
		if open {
			resultErr = errors.Join(resultErr, tx.Rollback())
		}
	}()
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS _schema_revision (
			id         INTEGER PRIMARY KEY CHECK (id = 1),
			revision   INTEGER NOT NULL CHECK (revision >= 0),
			updated_at TEXT NOT NULL,
			updated_by TEXT NOT NULL DEFAULT ''
		)`); err != nil {
		return state, fmt.Errorf("storage: schema revision prepare: create SQLite marker: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO _schema_revision (id, revision, updated_at, updated_by)
		VALUES (1, ?, datetime('now'), ?)
		ON CONFLICT (id) DO UPDATE SET
			revision   = excluded.revision,
			updated_at = excluded.updated_at,
			updated_by = excluded.updated_by
		WHERE _schema_revision.revision < excluded.revision`, SchemaRevision, stamp()); err != nil {
		return state, fmt.Errorf("storage: schema revision prepare: write SQLite marker: %w", err)
	}
	state.TableExists = true
	if err := tx.QueryRowContext(ctx,
		`SELECT revision, updated_by FROM _schema_revision WHERE id = 1`,
	).Scan(&state.Revision, &state.UpdatedBy); err != nil {
		return state, fmt.Errorf("storage: schema revision prepare: verify SQLite marker: %w", err)
	}
	state.Known = true
	if err := tx.Commit(); err != nil {
		open = false
		return state, fmt.Errorf("storage: schema revision prepare: commit SQLite marker: %w", err)
	}
	open = false
	return state, nil
}

// ProbePostgresSchemaRevision uses a plain pgx read-only transaction rather
// than Connect/ConnectWithPool, whose compatibility setup performs DDL.
func ProbePostgresSchemaRevision(ctx context.Context, dsn string) (state SchemaRevisionState, resultErr error) {
	conn, err := openSchemaRevisionPostgres(ctx, dsn)
	if err != nil {
		return state, err
	}
	defer func() { resultErr = errors.Join(resultErr, closeSchemaRevisionPostgres(conn)) }()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return state, fmt.Errorf("storage: schema revision probe: begin PostgreSQL read-only transaction: %w", err)
	}
	open := true
	defer func() {
		if open {
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			resultErr = errors.Join(resultErr, tx.Rollback(rollbackCtx))
		}
	}()
	schema, err := postgresCurrentSchema(ctx, tx)
	if err != nil {
		return state, err
	}
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM pg_catalog.pg_tables WHERE schemaname=$1 AND tablename=$2
	)`, schema, schemaRevisionTable).Scan(&state.TableExists); err != nil {
		return state, fmt.Errorf("storage: schema revision probe: inspect PostgreSQL marker table: %w", err)
	}
	if state.TableExists {
		q := "SELECT revision, updated_by FROM " + pgx.Identifier{schema, schemaRevisionTable}.Sanitize() + " WHERE id = 1"
		err = tx.QueryRow(ctx, q).Scan(&state.Revision, &state.UpdatedBy)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return state, fmt.Errorf("storage: schema revision probe: read PostgreSQL marker: %w", err)
		}
		state.Known = err == nil
	}
	if err := tx.Commit(ctx); err != nil {
		open = false
		return state, fmt.Errorf("storage: schema revision probe: finish PostgreSQL read-only transaction: %w", err)
	}
	open = false
	return state, nil
}

// PreparePostgresSchemaRevision atomically publishes the minimum reader
// revision before normal PostgreSQL connection setup or service-schema DDL.
// The caller must own the exclusive database lifetime lease.
func PreparePostgresSchemaRevision(ctx context.Context, dsn string) (state SchemaRevisionState, resultErr error) {
	conn, err := openSchemaRevisionPostgres(ctx, dsn)
	if err != nil {
		return state, err
	}
	defer func() { resultErr = errors.Join(resultErr, closeSchemaRevisionPostgres(conn)) }()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return state, fmt.Errorf("storage: schema revision prepare: begin PostgreSQL transaction: %w", err)
	}
	open := true
	defer func() {
		if open {
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			resultErr = errors.Join(resultErr, tx.Rollback(rollbackCtx))
		}
	}()
	schema, err := postgresCurrentSchema(ctx, tx)
	if err != nil {
		return state, err
	}
	table := pgx.Identifier{schema, schemaRevisionTable}.Sanitize()
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS `+table+` (
		id         INTEGER PRIMARY KEY CHECK (id = 1),
		revision   INTEGER NOT NULL CHECK (revision >= 0),
		updated_at TIMESTAMPTZ NOT NULL,
		updated_by TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		return state, fmt.Errorf("storage: schema revision prepare: create PostgreSQL marker: %w", err)
	}
	q := `INSERT INTO ` + table + ` AS marker (id, revision, updated_at, updated_by)
		VALUES (1, $1, CURRENT_TIMESTAMP, $2)
		ON CONFLICT (id) DO UPDATE SET
			revision   = excluded.revision,
			updated_at = excluded.updated_at,
			updated_by = excluded.updated_by
		WHERE marker.revision < excluded.revision`
	if _, err := tx.Exec(ctx, q, SchemaRevision, stamp()); err != nil {
		return state, fmt.Errorf("storage: schema revision prepare: write PostgreSQL marker: %w", err)
	}
	state.TableExists = true
	if err := tx.QueryRow(ctx, "SELECT revision, updated_by FROM "+table+" WHERE id = 1").Scan(
		&state.Revision, &state.UpdatedBy,
	); err != nil {
		return state, fmt.Errorf("storage: schema revision prepare: verify PostgreSQL marker: %w", err)
	}
	state.Known = true
	if err := tx.Commit(ctx); err != nil {
		open = false
		return state, fmt.Errorf("storage: schema revision prepare: commit PostgreSQL marker: %w", err)
	}
	open = false
	return state, nil
}

func openSchemaRevisionPostgres(ctx context.Context, dsn string) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: schema revision probe: parse PostgreSQL DSN: %w", err)
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("storage: schema revision probe: connect PostgreSQL read-only: %w", err)
	}
	return conn, nil
}

func closeSchemaRevisionPostgres(conn *pgx.Conn) error {
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return conn.Close(closeCtx)
}

type schemaRevisionPGQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func postgresCurrentSchema(ctx context.Context, q schemaRevisionPGQuerier) (string, error) {
	var schema string
	if err := q.QueryRow(ctx, `SELECT COALESCE(current_schema(), '')`).Scan(&schema); err != nil {
		return "", fmt.Errorf("storage: schema revision: resolve PostgreSQL current_schema: %w", err)
	}
	if schema == "" {
		return "", errors.New("storage: schema revision: PostgreSQL current_schema is empty")
	}
	return schema, nil
}

func schemaRevisionSQLiteURI(path, query string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p, RawQuery: query}).String()
}
