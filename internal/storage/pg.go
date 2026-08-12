package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB is the database handle abstraction. It carries either a PostgreSQL
// pgxpool.Pool or a SQLite *sql.DB, plus the matching Dialect. All Exec/Query
// methods route to the right backend transparently.
type DB struct {
	pool         *pgxpool.Pool // non-nil for PG
	sqlDB        *sql.DB       // non-nil for SQLite
	databaseFile string        // absolute SQLite database path; empty for PostgreSQL/in-memory
	filesDir     string
	dialect      Dialect
	blobStore    BlobObjectStore // non-nil when file_storage=s3 configured
	blobPrefix   string          // key prefix for blob objects in the bucket
	blobStream   bool            // s3 attachments: stream via Range instead of temp file
	// rlsGuard — strict-RLS чокпоинт (план 79F). nil = выключен (по умолчанию).
	// Когда задан, List для сущности, у которой guard возвращает true (есть
	// строковая политика), но без вычисленного строкового доступа
	// (ListParams.RowFilterEvaluated=false), отклоняется fail-closed. Инжектится
	// из лаунчера/сервера в строгом режиме (ONEBASE_STRICT_RLS).
	rlsGuard func(entityName string) bool
	// schemaOpts — режим реструктуризации схемы (план 81): пробный прогон,
	// разрешение на удаление колонок, приёмник отчёта. Нулевое значение —
	// прежнее поведение: применять всё безопасное, лишние колонки не трогать.
	schemaOpts SchemaOptions
	// ftsState — есть ли в базе схема полнотекстового поиска (план 82).
	// Кэш на процесс: без него запись объекта в базу, ещё не прошедшую
	// migrate, падала бы на INSERT в несуществующий _fts. См. ftsAvailable.
	ftsState int32
	// ftsCfg — имя конфигурации текстового поиска PostgreSQL (russian/simple).
	ftsCfg     atomic.Value
	closeMu    sync.Mutex
	closeHooks []func() error
	closed     bool
}

// SetStrictRLSGuard включает strict-RLS чокпоинт (план 79F, defense-in-depth).
// guard(entityName) == true означает «у сущности есть строковая политика».
// Передача nil выключает режим. Возвращает db для чейнинга при инициализации.
func (db *DB) SetStrictRLSGuard(guard func(entityName string) bool) *DB {
	db.rlsGuard = guard
	return db
}

// BlobObjectStore is the S3-compatible backend used when file_storage=s3
// (план 110, этап 2). objstore.Client satisfies it structurally, so storage
// does not import objstore — the client is injected via SetBlobStore from the
// CLI, where app.yaml (file_storage.s3) is available.
type BlobObjectStore interface {
	PutObject(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	GetObject(ctx context.Context, key string) (io.ReadCloser, int64, error)
	DeleteObject(ctx context.Context, key string) error
	// OpenReadSeeker returns a lazy, Range-backed seekable reader over key (size
	// is the known object size). Used to stream S3 attachments via
	// http.ServeContent without a temp copy, when streaming is enabled.
	OpenReadSeeker(ctx context.Context, key string, size int64) io.ReadSeekCloser
}

// SetBlobStore attaches an S3-compatible object store for blob content and the
// key prefix under which blobs live. Passing nil disables S3 (falls back to the
// configured disk/db mode).
func (db *DB) SetBlobStore(store BlobObjectStore, prefix string) {
	db.blobStore = store
	db.blobPrefix = strings.Trim(prefix, "/")
}

// SetBlobStreaming toggles streaming S3 attachment downloads via Range requests
// (file_storage.s3.stream) instead of materializing a temp file for serving.
// Off by default: the temp-file path is simpler and decouples S3 fetch speed
// from a slow client. Does not affect image blobs (already streamed) or DSL
// ПутьКВложению (always materialized to a real path).
func (db *DB) SetBlobStreaming(on bool) { db.blobStream = on }

// blobObjectKey builds the bucket key for a blob id: [<prefix>/]blobs/<id>.
func (db *DB) blobObjectKey(id uuid.UUID) string {
	if db.blobPrefix == "" {
		return "blobs/" + id.String()
	}
	return db.blobPrefix + "/blobs/" + id.String()
}

func pgQuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// PoolConfig carries optional PostgreSQL connection-pool sizing. Zero-valued
// fields fall back to OneBase defaults (see applyPoolDefaults), which in turn
// yield to explicit pool_* parameters already present in the DSN. Applies to
// PostgreSQL only — SQLite uses a single connection. План 111 (P0-1).
type PoolConfig struct {
	MaxConns int32 // 0 = default (defaultPoolMaxConns), unless set in the DSN
	MinConns int32 // 0 = default (defaultPoolMinConns), unless set in the DSN
}

// Sane pool defaults for one onebase process serving many users. pgx's own
// default is max(4, NumCPU) ≈ 4–8, which starves the hot path under concurrency
// (auth round-trips per request, document posting holding a connection for the
// whole transaction). See Plans/111-scalability-review.md §3.1.
const (
	defaultPoolMaxConns = 20
	defaultPoolMinConns = 2
)

// Connect opens a PostgreSQL pool with OneBase's default pool sizing. Use it for
// short-lived CLI commands; the server uses ConnectWithPool to honor app.yaml.
func Connect(ctx context.Context, dsn string) (*DB, error) {
	return ConnectWithPool(ctx, dsn, PoolConfig{})
}

// ConnectWithPool opens a PostgreSQL pool, sizing it from pc with precedence
// app.yaml (pc) > explicit pool_* in the DSN > OneBase defaults.
func ConnectWithPool(ctx context.Context, dsn string, pc PoolConfig) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: parse dsn: %w", err)
	}
	applyPoolDefaults(cfg, dsn, pc)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("storage: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
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

// applyPoolDefaults sets pool sizing on cfg. ParseConfig has already applied
// pgx's own auto-default to cfg.MaxConns/MinConns, so we override only when
// neither app.yaml (pc) nor the DSN spoke, preserving an operator's explicit
// pool_max_conns/pool_min_conns in the DSN.
func applyPoolDefaults(cfg *pgxpool.Config, dsn string, pc PoolConfig) {
	switch {
	case pc.MaxConns > 0:
		cfg.MaxConns = pc.MaxConns
	case !dsnHasPoolParam(dsn, "pool_max_conns"):
		cfg.MaxConns = defaultPoolMaxConns
	}
	switch {
	case pc.MinConns > 0:
		cfg.MinConns = pc.MinConns
	case !dsnHasPoolParam(dsn, "pool_min_conns"):
		cfg.MinConns = defaultPoolMinConns
	}
	// MinConns must not exceed MaxConns, or pgxpool.NewWithConfig rejects it.
	if cfg.MinConns > cfg.MaxConns {
		cfg.MinConns = cfg.MaxConns
	}
}

// dsnHasPoolParam reports whether the DSN explicitly sets the given pgxpool
// parameter (URL query or keyword/value form both contain "<param>=").
func dsnHasPoolParam(dsn, param string) bool {
	return strings.Contains(dsn, param+"=")
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

// SQLitePath returns the exact on-disk SQLite database opened by this DB.
// An empty value means PostgreSQL or an in-memory SQLite database.
func (db *DB) SQLitePath() string { return db.databaseFile }

// SetFilesDir переопределяет каталог файлового хранилища (вложения, блобы).
func (db *DB) SetFilesDir(dir string) { db.filesDir = dir }

func (db *DB) Close() {
	if db == nil {
		return
	}
	db.closeMu.Lock()
	if db.closed {
		db.closeMu.Unlock()
		return
	}
	db.closed = true
	hooks := db.closeHooks
	db.closeHooks = nil
	db.closeMu.Unlock()
	webhookURLScrubbed.Delete(db)
	if db.pool != nil {
		db.pool.Close()
	}
	if db.sqlDB != nil {
		_ = db.sqlDB.Close()
	}
	for i := len(hooks) - 1; i >= 0; i-- {
		_ = hooks[i]()
	}
}

// AddCloseHook binds an external lifetime resource (for example a database
// coordination lease) to this handle. Hooks run after all pool connections are
// closed, in reverse registration order. A hook added after Close runs now.
func (db *DB) AddCloseHook(hook func() error) {
	if db == nil || hook == nil {
		return
	}
	db.closeMu.Lock()
	if db.closed {
		db.closeMu.Unlock()
		_ = hook()
		return
	}
	db.closeHooks = append(db.closeHooks, hook)
	db.closeMu.Unlock()
}

// DisableFKForImport disables foreign-key constraint enforcement for the
// duration of a bulk import and returns a cleanup function that re-enables it.
//
// SQLite: pins the connection pool to 1 connection so that the PRAGMA applies
// to every subsequent statement, then executes PRAGMA foreign_keys=OFF.
// The cleanup restores PRAGMA foreign_keys=ON and the pool size.
//
// PostgreSQL: drops all FK constraints via ALTER TABLE (DDL). Callers that
// need crash-safe bulk import invoke this with a transaction-bearing context,
// so the drop, data replacement, validation and restoration commit atomically.
// Previously we used
// SET session_replication_role='replica', but that is a session-level setting
// that only applies to ONE connection — other pool connections still enforce
// FK constraints (including ON DELETE CASCADE), causing silent data loss
// when import reorders tables and intermittent FK violation errors.
func (db *DB) DisableFKForImport(ctx context.Context) (cleanup func() error, err error) {
	if db.sqlDB != nil {
		db.sqlDB.SetMaxOpenConns(1)
		if _, err := db.Exec(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
			// ConnectSQLite deliberately uses one connection so per-connection
			// PRAGMAs remain authoritative after import as well.
			db.sqlDB.SetMaxOpenConns(1)
			return func() error { return nil }, err
		}
		cleanupCtx := context.WithoutCancel(ctx)
		return func() error {
			_, err := db.Exec(cleanupCtx, "PRAGMA foreign_keys=ON")
			if errors.Is(err, ErrDurableSessionClosed) {
				// Commit-outcome resolution deliberately discards the ambiguous
				// connection. A replacement connection is initialized with the same
				// operational PRAGMAs by the SQLite DSN.
				_, err = db.Exec(WithoutDurableSession(cleanupCtx), "PRAGMA foreign_keys=ON")
			}
			db.sqlDB.SetMaxOpenConns(1)
			return err
		}, nil
	}
	// PostgreSQL: route all DDL through DB so a transaction carried by ctx is
	// honored. A restore must not expose a crash window with constraints absent.
	type fkInfo struct {
		table     string
		name      string
		def       string
		validated bool
	}
	// Use pg_class.relname (always unquoted) instead of regclass::text
	// which may return quoted identifiers like "возвратотпокупателя",
	// causing double-quoting and silent ALTER TABLE failures.
	rows, err := db.Query(ctx,
		`SELECT c.conname, t.relname, pg_get_constraintdef(c.oid), c.convalidated
		 FROM pg_constraint c
		 JOIN pg_class t ON c.conrelid = t.oid
		 WHERE c.contype='f' AND c.connamespace=current_schema()::regnamespace`)
	if err != nil {
		return func() error { return nil }, err
	}
	var fks []fkInfo
	for rows.Next() {
		var name, table, def string
		var validated bool
		if err := rows.Scan(&name, &table, &def, &validated); err != nil {
			rows.Close()
			return func() error { return nil }, err
		}
		fks = append(fks, fkInfo{table: table, name: name, def: def, validated: validated})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return func() error { return nil }, err
	}

	restore := func(items []fkInfo) error {
		var errs []error
		var restored []fkInfo
		for _, fk := range items {
			tq := `"` + strings.ReplaceAll(fk.table, `"`, `""`) + `"`
			nq := `"` + strings.ReplaceAll(fk.name, `"`, `""`) + `"`
			definition := fk.def
			if !strings.Contains(strings.ToUpper(definition), "NOT VALID") {
				definition += " NOT VALID"
			}
			if _, err := db.Exec(ctx,
				"ALTER TABLE "+tq+" ADD CONSTRAINT "+nq+" "+definition); err != nil {
				errs = append(errs, fmt.Errorf("restore FK %s.%s: %w", fk.table, fk.name, err))
				continue
			}
			restored = append(restored, fk)
		}
		// Preserve originally-unvalidated constraints as-is, but do not silently
		// downgrade validated source constraints after bulk import.
		for _, fk := range restored {
			if !fk.validated {
				continue
			}
			tq := `"` + strings.ReplaceAll(fk.table, `"`, `""`) + `"`
			nq := `"` + strings.ReplaceAll(fk.name, `"`, `""`) + `"`
			if _, err := db.Exec(ctx, "ALTER TABLE "+tq+" VALIDATE CONSTRAINT "+nq); err != nil {
				errs = append(errs, fmt.Errorf("validate FK %s.%s: %w", fk.table, fk.name, err))
			}
		}
		return errors.Join(errs...)
	}
	var dropped []fkInfo
	for _, fk := range fks {
		tq := `"` + strings.ReplaceAll(fk.table, `"`, `""`) + `"`
		nq := `"` + strings.ReplaceAll(fk.name, `"`, `""`) + `"`
		if _, err := db.Exec(ctx, "ALTER TABLE "+tq+" DROP CONSTRAINT "+nq); err != nil {
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
