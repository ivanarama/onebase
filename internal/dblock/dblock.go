// Package dblock coordinates the lifetime of OneBase processes that use the
// same database. The locks are deliberately independent from storage so they
// can be acquired before a database pool is opened and held until after it is
// closed.
package dblock

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/jackc/pgx/v5"
)

// ErrLocked means another cooperating OneBase process owns the database
// lifetime lock. Callers should fail closed instead of opening or replacing
// the database while this error is present.
var ErrLocked = errors.New("database is already in use by another OneBase process")

// Lease is held for the complete lifetime of a database user or destructive
// restore. Close releases it. Implementations make Close idempotent.
type Lease interface {
	Close() error
	// Downgrade converts an exclusive lease to a shared consumer lease. The
	// conversion may briefly release the OS primitive; callers must have no
	// database handle open until it returns, then recheck recovery state.
	Downgrade(context.Context) error
}

type noopLease struct{}

func (noopLease) Close() error                    { return nil }
func (noopLease) Downgrade(context.Context) error { return nil }

func isMemorySQLite(path string) bool {
	if path == ":memory:" {
		return true
	}
	low := strings.ToLower(path)
	return strings.HasPrefix(low, "file::memory:") ||
		(strings.HasPrefix(low, "file:") && strings.Contains(low, "mode=memory"))
}

// CanonicalSQLitePath returns the stable on-disk target used for a SQLite
// lifetime lock. Existing symlinks are resolved so a registered alias cannot
// acquire a different lock for the same file.
func CanonicalSQLitePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("sqlite database path is empty")
	}
	if isMemorySQLite(path) {
		return path, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("canonicalize sqlite database path: %w", err)
	}
	abs = filepath.Clean(abs)
	if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
		return filepath.Clean(resolved), nil
	} else if !errors.Is(resolveErr, os.ErrNotExist) {
		return "", fmt.Errorf("resolve sqlite database path %q: %w", abs, resolveErr)
	}

	// EvalSymlinks requires the final file to exist. New databases still need
	// one canonical lock when an existing parent directory is itself a symlink.
	// Resolve a dangling final symlink explicitly, then otherwise resolve the
	// longest existing parent and append the missing suffix again.
	if info, lstatErr := os.Lstat(abs); lstatErr == nil && info.Mode()&os.ModeSymlink != 0 {
		target, readErr := os.Readlink(abs)
		if readErr != nil {
			return "", fmt.Errorf("read sqlite database symlink %q: %w", abs, readErr)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(abs), target)
		}
		return CanonicalSQLitePath(target)
	}

	current := abs
	var missing []string
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("resolve sqlite database path %q: no existing parent", abs)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
		resolved, resolveErr := filepath.EvalSymlinks(current)
		if resolveErr != nil {
			if errors.Is(resolveErr, os.ErrNotExist) {
				continue
			}
			return "", fmt.Errorf("resolve sqlite database parent %q: %w", current, resolveErr)
		}
		for i := len(missing) - 1; i >= 0; i-- {
			resolved = filepath.Join(resolved, missing[i])
		}
		return filepath.Clean(resolved), nil
	}
}

// AcquireSQLiteTarget obtains a non-blocking cross-process exclusive lock and
// returns the exact canonical database path protected by it. Callers must open
// or replace returnedPath, not resolve the original path a second time: a
// symlink could otherwise be retargeted between locking and use.
func AcquireSQLiteTarget(path string) (lease Lease, returnedPath string, err error) {
	canonical, err := CanonicalSQLitePath(path)
	if err != nil {
		return nil, "", err
	}
	if isMemorySQLite(canonical) {
		return noopLease{}, canonical, nil
	}
	parent, err := validatedSQLiteTargetParent(canonical)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(parent, fsmode.Dir); err != nil { //nolint:gosec // G703: parent is derived from the absolute, clean, symlink-resolved SQLite target validated above; it is the operator-selected database directory, not a joined child path
		return nil, "", fmt.Errorf("create sqlite database directory: %w", err)
	}
	lockPath := canonical + ".onebase.lock"
	fileLock, acquired, err := tryAcquireFileLease(lockPath, false)
	if err != nil {
		return nil, "", fmt.Errorf("acquire sqlite lifetime lock %q: %w", lockPath, err)
	}
	if !acquired {
		return nil, "", fmt.Errorf("%w: SQLite database %s", ErrLocked, canonical)
	}
	return fileLock, canonical, nil
}

// AcquireSQLiteSharedTarget obtains a shared lifetime lease for an ordinary
// database consumer. Destructive restore uses AcquireSQLiteTarget (exclusive),
// so it cannot begin until every cooperating pool has closed.
func AcquireSQLiteSharedTarget(path string) (lease Lease, returnedPath string, err error) {
	canonical, err := CanonicalSQLitePath(path)
	if err != nil {
		return nil, "", err
	}
	if isMemorySQLite(canonical) {
		return noopLease{}, canonical, nil
	}
	parent, err := validatedSQLiteTargetParent(canonical)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(parent, fsmode.Dir); err != nil { //nolint:gosec // validated operator-selected database parent
		return nil, "", fmt.Errorf("create sqlite database directory: %w", err)
	}
	lockPath := canonical + ".onebase.lock"
	fileLock, acquired, err := tryAcquireFileLease(lockPath, true)
	if err != nil {
		return nil, "", fmt.Errorf("acquire shared sqlite lifetime lock %q: %w", lockPath, err)
	}
	if !acquired {
		return nil, "", fmt.Errorf("%w: SQLite database %s", ErrLocked, canonical)
	}
	return fileLock, canonical, nil
}

func validatedSQLiteTargetParent(canonical string) (string, error) {
	if canonical == "" || !filepath.IsAbs(canonical) || filepath.Clean(canonical) != canonical {
		return "", fmt.Errorf("invalid canonical sqlite database path %q", canonical)
	}
	parent := filepath.Dir(canonical)
	if parent == canonical || filepath.Base(canonical) == "." || filepath.Base(canonical) == string(filepath.Separator) {
		return "", fmt.Errorf("unsafe sqlite database target %q", canonical)
	}
	return parent, nil
}

// AcquireSQLite obtains a database lifetime lock when the caller does not need
// the canonical target (for example, when it only needs mutual exclusion).
func AcquireSQLite(path string) (Lease, error) {
	lease, _, err := AcquireSQLiteTarget(path)
	return lease, err
}

func AcquireSQLiteShared(path string) (Lease, error) {
	lease, _, err := AcquireSQLiteSharedTarget(path)
	return lease, err
}

type postgresIdentity struct {
	Host     string
	Port     uint16
	Database string
}

func normalizedPostgresIdentity(dsn string) (postgresIdentity, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return postgresIdentity{}, fmt.Errorf("parse PostgreSQL DSN: %w", err)
	}
	host := strings.TrimSpace(cfg.Host)
	// DNS host names are case-insensitive. Unix-domain socket paths are not.
	if !filepath.IsAbs(host) {
		host = strings.TrimSuffix(strings.ToLower(host), ".")
		if ip := net.ParseIP(host); ip != nil {
			host = ip.String()
		}
	}
	return postgresIdentity{
		Host:     host,
		Port:     cfg.Port,
		Database: cfg.Database,
	}, nil
}

// CanonicalPostgresIdentity identifies the database target while ignoring all
// credentials and option ordering. Different users can still address the same
// database and must therefore be treated as aliases for destructive restore
// and whole-database export operations.
func CanonicalPostgresIdentity(dsn string) (string, error) {
	id, err := normalizedPostgresIdentity(dsn)
	if err != nil {
		return "", err
	}
	// Length prefixes avoid delimiter ambiguity in database/user names.
	parts := []string{id.Host, fmt.Sprint(id.Port), id.Database}
	var b strings.Builder
	for _, part := range parts {
		fmt.Fprintf(&b, "%d:%s", len(part), part)
	}
	return b.String(), nil
}

func postgresAdvisoryKey(database string) int64 {
	sum := sha256.Sum256([]byte("onebase-database-lifetime-v1\x00" + database))
	return int64(binary.BigEndian.Uint64(sum[:8])) //nolint:gosec // advisory locks accept the full signed 64-bit key space
}

type postgresLease struct {
	mu     sync.Mutex
	conn   *pgx.Conn
	key    int64
	shared bool
}

// AcquirePostgres opens a dedicated connection and takes a session advisory
// lock keyed by current_database(). All OneBase processes that connect to the
// same database on a PostgreSQL cluster therefore contend on the same lock,
// even when their DSNs use different users or option ordering.
func AcquirePostgres(ctx context.Context, dsn string) (Lease, error) {
	return acquirePostgres(ctx, dsn, false)
}

// AcquirePostgresShared takes a session-level shared advisory lock for an
// ordinary consumer. PostgreSQL continues to coordinate normal concurrent
// readers/writers itself, while an exclusive restore waits for all consumers.
func AcquirePostgresShared(ctx context.Context, dsn string) (Lease, error) {
	return acquirePostgres(ctx, dsn, true)
}

func acquirePostgres(ctx context.Context, dsn string, shared bool) (Lease, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL DSN for lifetime lock: %w", err)
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect PostgreSQL for lifetime lock: %w", err)
	}
	closeOnError := func(cause error) (Lease, error) {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return nil, errors.Join(cause, conn.Close(closeCtx))
	}
	var database string
	if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil {
		return closeOnError(fmt.Errorf("read PostgreSQL database identity: %w", err))
	}
	key := postgresAdvisoryKey(database)
	query := "SELECT pg_try_advisory_lock($1)"
	if shared {
		query = "SELECT pg_try_advisory_lock_shared($1)"
	}
	var acquired bool
	if err := conn.QueryRow(ctx, query, key).Scan(&acquired); err != nil {
		return closeOnError(fmt.Errorf("acquire PostgreSQL lifetime lock for %q: %w", database, err))
	}
	if !acquired {
		return closeOnError(fmt.Errorf("%w: PostgreSQL database %s", ErrLocked, database))
	}
	return &postgresLease{conn: conn, key: key, shared: shared}, nil
}

func (l *postgresLease) Downgrade(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn == nil || l.shared {
		return nil
	}
	var unlocked bool
	if err := l.conn.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", l.key).Scan(&unlocked); err != nil {
		return err
	}
	if !unlocked {
		return errors.New("database lifetime lock was not owned during downgrade")
	}
	// Blocking acquisition closes the handoff gap: another exclusive operation
	// may win after unlock, but this consumer cannot open the DB until it exits.
	if _, err := l.conn.Exec(ctx, "SELECT pg_advisory_lock_shared($1)", l.key); err != nil {
		return err
	}
	l.shared = true
	return nil
}

func (l *postgresLease) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn == nil {
		return nil
	}
	conn := l.conn
	l.conn = nil
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Closing the owning session releases every advisory lock even if an
	// explicit unlock query could not be delivered because the connection died.
	return conn.Close(ctx)
}
