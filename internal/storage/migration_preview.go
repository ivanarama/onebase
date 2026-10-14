package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

// WithPostgresMigrationPreview never calls normal connection setup (which
// creates a uuid cast). Configuration, revision and plans share one read-only
// repeatable snapshot. Even a caller bug cannot execute DDL in this callback.
func WithPostgresMigrationPreview(ctx context.Context, dsn string, fn func(context.Context, *DB) error) error {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return err
	}
	cfg.MaxConns = 1
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	db := &DB{pool: pool, dialect: PgDialect{}}
	return db.WithReadSnapshot(ctx, func(ctx context.Context) error { return fn(ctx, db) })
}

func previewSQLiteFile(path string) (os.FileInfo, [32]byte, error) {
	var digest [32]byte
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); err == nil {
			return nil, digest, fmt.Errorf("preview refuses SQLite sidecar %s: run ordinary recovery outside --dry-run", suffix)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, digest, err
		}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, digest, nil
	}
	if err != nil {
		return nil, digest, err
	}
	if !info.Mode().IsRegular() {
		return nil, digest, errors.New("preview SQLite target is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, digest, err
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, f)
	closeErr := f.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return nil, digest, err
	}
	copy(digest[:], h.Sum(nil))
	return info, digest, nil
}

// WithSQLiteMigrationPreview requires a lifetime lease already acquired by the
// caller. WAL/journal refusal happens before SQLite can recover anything. Only
// a stable sidecar-free file is opened immutable; missing targets use memory.
func WithSQLiteMigrationPreview(ctx context.Context, path string, existed bool, fn func(context.Context, *DB) error) (resultErr error) {
	var before os.FileInfo
	var hash [32]byte
	var err error
	memory := IsInMemorySQLitePath(path)
	if !memory {
		before, hash, err = previewSQLiteFile(path)
		if err != nil {
			return err
		}
	}
	if !memory && (before != nil) != existed {
		return errors.New("SQLite target appeared or disappeared before preview")
	}
	dsn := ":memory:"
	if before != nil {
		dsn = schemaRevisionSQLiteURI(path, "mode=ro&immutable=1")
	}
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	defer func() { resultErr = errors.Join(resultErr, conn.Close()) }()
	if _, err := conn.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
		return err
	}
	db := &DB{sqlDB: conn, dialect: SQLiteDialect{}}
	resultErr = db.WithReadSnapshot(ctx, func(ctx context.Context) error { return fn(ctx, db) })
	if !memory {
		after, afterHash, err := previewSQLiteFile(path)
		if err != nil {
			return errors.Join(resultErr, err)
		}
		if (before == nil) != (after == nil) || before != nil && (!os.SameFile(before, after) || hash != afterHash) {
			return errors.Join(resultErr, errors.New("SQLite database changed during preview"))
		}
	}
	return resultErr
}
