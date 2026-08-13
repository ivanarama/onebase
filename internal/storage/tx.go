package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type txKey struct{}

type durableSessionKey struct{}

var ErrDurableSessionClosed = errors.New("durable session is closed")

const durableSessionCleanupTimeout = 5 * time.Second

// DurableSession pins one backend connection with durable commit semantics.
// Passing its Context to DB operations and BeginTx keeps the whole
// cross-resource protocol (pending marker, transaction, resolution, marker
// deletion) on that exact connection.
type DurableSession struct {
	db             *DB
	sqliteConn     *sql.Conn
	pgConn         *pgxpool.Conn
	sqlitePrevious int
	pgPrevious     string
	ctx            context.Context
	done           atomic.Bool
}

func (db *DB) BeginDurableSession(ctx context.Context) (*DurableSession, error) {
	session := &DurableSession{db: db, ctx: ctx}
	switch {
	case db.sqlDB != nil:
		conn, err := db.sqlDB.Conn(ctx)
		if err != nil {
			return nil, err
		}
		var previous int
		if err := conn.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&previous); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("sqlite durable session: inspect synchronous mode: %w", err)
		}
		if _, err := conn.ExecContext(ctx, "PRAGMA synchronous=FULL"); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("sqlite durable session: enable synchronous=FULL: %w", err)
		}
		session.sqliteConn = conn
		session.sqlitePrevious = previous
	case db.pool != nil:
		conn, err := db.pool.Acquire(ctx)
		if err != nil {
			return nil, err
		}
		var previous string
		if err := conn.QueryRow(ctx, "SHOW synchronous_commit").Scan(&previous); err != nil {
			conn.Release()
			return nil, fmt.Errorf("postgres durable session: inspect synchronous_commit: %w", err)
		}
		if err := setPostgresSynchronousCommit(ctx, conn.Exec, "on", false); err != nil {
			conn.Release()
			return nil, fmt.Errorf("postgres durable session: enable synchronous_commit: %w", err)
		}
		session.pgConn = conn
		session.pgPrevious = previous
	default:
		return nil, errors.New("durable session: no database connection")
	}
	session.ctx = context.WithValue(ctx, durableSessionKey{}, session)
	return session, nil
}

func (session *DurableSession) Context() context.Context {
	if session == nil {
		return context.Background()
	}
	return session.ctx
}

func (session *DurableSession) Close() error {
	if session == nil || !session.done.CompareAndSwap(false, true) {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), durableSessionCleanupTimeout)
	defer cancel()
	if session.sqliteConn != nil {
		restoreErr := restoreSQLiteSynchronous(cleanupCtx, session.sqliteConn, session.sqlitePrevious)
		if restoreErr != nil {
			// A pooled SQLite connection whose synchronous mode could not be
			// restored must not be reused with an unknown operational profile.
			if rawErr := session.sqliteConn.Raw(func(any) error { return driver.ErrBadConn }); rawErr != nil &&
				!errors.Is(rawErr, driver.ErrBadConn) {
				restoreErr = errors.Join(restoreErr, rawErr)
			}
		}
		closeErr := session.sqliteConn.Close()
		return errors.Join(restoreErr, closeErr)
	}
	if session.pgConn != nil {
		restoreErr := setPostgresSynchronousCommit(
			cleanupCtx, session.pgConn.Exec, session.pgPrevious, false,
		)
		if restoreErr != nil {
			restoreErr = errors.Join(restoreErr, session.pgConn.Conn().PgConn().Close(cleanupCtx))
		}
		session.pgConn.Release()
		return restoreErr
	}
	return nil
}

// Discard closes the physical backend connection without restoring its prior
// session settings. Use it after an ambiguous commit: executing any further
// command on that connection, including a setting reset, is unsafe.
func (session *DurableSession) Discard(ctx context.Context) error {
	if session == nil || !session.done.CompareAndSwap(false, true) {
		return nil
	}
	if session.sqliteConn != nil {
		rawErr := session.sqliteConn.Raw(func(any) error { return driver.ErrBadConn })
		if errors.Is(rawErr, driver.ErrBadConn) || errors.Is(rawErr, sql.ErrConnDone) {
			rawErr = nil
		}
		closeErr := session.sqliteConn.Close()
		if errors.Is(closeErr, sql.ErrConnDone) {
			closeErr = nil
		}
		return errors.Join(rawErr, closeErr)
	}
	if session.pgConn != nil {
		// PgConn.Close always closes the underlying net.Conn even if the clean
		// Terminate exchange reports an error. Once released, pgxpool destroys
		// the closed connection instead of returning it to the idle set.
		_ = session.pgConn.Conn().PgConn().Close(ctx)
		session.pgConn.Release()
		return nil
	}
	return nil
}

func durableSessionFromContext(ctx context.Context, db *DB) *DurableSession {
	session, _ := ctx.Value(durableSessionKey{}).(*DurableSession)
	if session == nil || session.db != db {
		return nil
	}
	return session
}

// DurableSessionFromContext exposes the session only to infrastructure which
// must explicitly retire a connection after an ambiguous commit outcome.
func DurableSessionFromContext(ctx context.Context, db *DB) *DurableSession {
	return durableSessionFromContext(ctx, db)
}

// WithoutDurableSession returns a child context which deliberately cannot
// reuse a pinned DurableSession. It is intended for commit-outcome resolution:
// after an ambiguous Commit the original connection may be unusable, so the
// barrier transaction must acquire an independent backend connection.
func WithoutDurableSession(ctx context.Context) context.Context {
	return context.WithValue(ctx, durableSessionKey{}, (*DurableSession)(nil))
}

func (session *DurableSession) ensureDurable(ctx context.Context) error {
	if session == nil || session.done.Load() {
		return ErrDurableSessionClosed
	}
	if session.sqliteConn != nil {
		var mode int
		if err := session.sqliteConn.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&mode); err != nil {
			return err
		}
		if mode != 2 {
			_, err := session.sqliteConn.ExecContext(ctx, "PRAGMA synchronous=FULL")
			return err
		}
		return nil
	}
	if session.pgConn != nil {
		return setPostgresSynchronousCommit(ctx, session.pgConn.Exec, "on", false)
	}
	return errors.New("durable session has no connection")
}

type postgresExecFunc func(context.Context, string, ...any) (pgconn.CommandTag, error)

func setPostgresSynchronousCommit(ctx context.Context, exec postgresExecFunc, mode string, local bool) error {
	if strings.TrimSpace(mode) == "" {
		return errors.New("postgres durable session: empty synchronous_commit mode")
	}
	scope := "false"
	if local {
		scope = "true"
	}
	_, err := exec(ctx, "SELECT set_config('synchronous_commit', $1, "+scope+")", mode)
	return err
}

func verifyPostgresSynchronousCommit(ctx context.Context, queryRow func(context.Context, string, ...any) pgx.Row) error {
	var mode string
	if err := queryRow(ctx, "SHOW synchronous_commit").Scan(&mode); err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(mode), "off") {
		return errors.New("postgres durable session: synchronous_commit was downgraded to off")
	}
	return nil
}

type pgExecutionBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

var txScopeSequence atomic.Uint64

// IsNotFound reports the portable no-row condition for both storage drivers.
func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows)
}

// HasTx reports whether ctx already carries an active storage transaction.
func HasTx(ctx context.Context) bool {
	return ctx.Value(txKey{}) != nil
}

// WithReadSnapshot выполняет fn на согласованном снимке данных: все чтения
// внутри видят базу в одном состоянии.
//
// Нужно там, где подряд читают несколько таблиц и результат обязан быть
// согласован между ними, — прежде всего выгрузке резервной копии. Без общего
// снимка архив собирается последовательными autocommit-запросами: объект
// успевает попасть в файл до перехода на следующий этап, а его история — уже
// после, и восстановленная база показывает состояние, которого никогда не было.
//
// На PostgreSQL это REPEATABLE READ READ ONLY (инструкция обязана быть первой в
// транзакции). На SQLite отдельного уровня нет: одна читающая транзакция сама
// удерживает согласованное чтение до конца.
//
// Внутри чужой транзакции снимок уже зафиксирован — fn выполняется как есть.
func (db *DB) WithReadSnapshot(ctx context.Context, fn func(context.Context) error) error {
	if HasTx(ctx) {
		return fn(ctx)
	}
	return db.WithTx(ctx, func(txCtx context.Context) error {
		if db.IsPostgres() {
			if _, err := db.Exec(txCtx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY"); err != nil {
				return fmt.Errorf("read snapshot: %w", err)
			}
		}
		return fn(txCtx)
	})
}

// WithTxIfNeeded joins an existing storage transaction or starts a new one.
// It is the safe entry point for write paths callable both from HTTP and from
// DSL code that may already run inside an explicit transaction.
func (db *DB) WithTxIfNeeded(ctx context.Context, fn func(context.Context) error) error {
	if HasTx(ctx) {
		return fn(ctx)
	}
	return db.WithTx(ctx, fn)
}

// WithTxScope makes fn atomic even when ctx already carries a transaction.
// A top-level call owns a regular transaction; a nested/borrowed call owns a
// savepoint, so returning an error cannot leave provisional rows or hook side
// effects in the caller's transaction.
func (db *DB) WithTxScope(ctx context.Context, fn func(context.Context) error) (err error) {
	if !HasTx(ctx) {
		return db.WithTx(ctx, fn)
	}

	savepoint := fmt.Sprintf("onebase_scope_%d", txScopeSequence.Add(1))
	if _, err := db.Exec(ctx, "SAVEPOINT "+savepoint); err != nil {
		return fmt.Errorf("create savepoint %s: %w", savepoint, err)
	}
	PushTxHookScope(ctx)

	rollback := func() error {
		_, rollbackErr := db.Exec(ctx, "ROLLBACK TO SAVEPOINT "+savepoint)
		_, releaseErr := db.Exec(ctx, "RELEASE SAVEPOINT "+savepoint)
		RollbackTxHookScope(ctx)
		return errors.Join(rollbackErr, releaseErr)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = rollback()
			panic(p)
		}
	}()

	if err = fn(ctx); err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("rollback savepoint %s: %w", savepoint, rollbackErr))
		}
		return err
	}
	if _, err = db.Exec(ctx, "RELEASE SAVEPOINT "+savepoint); err != nil {
		RollbackTxHookScope(ctx)
		return fmt.Errorf("release savepoint %s: %w", savepoint, err)
	}
	CommitTxHookScope(ctx)
	return nil
}

// WithTx runs fn inside a transaction. On fn error the transaction is rolled
// back; on success it is committed.
func (db *DB) WithTx(ctx context.Context, fn func(context.Context) error) (err error) {
	tx, txCtx, berr := db.BeginTx(ctx)
	if berr != nil {
		return berr
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(txCtx)
			panic(p)
		}
	}()
	if err = fn(txCtx); err != nil {
		_ = tx.Rollback(txCtx)
		return err
	}
	return tx.Commit(txCtx)
}

// ContextWithTx embeds a storage.Tx into ctx so that exec/q/Exec/Query use it.
func ContextWithTx(ctx context.Context, tx Tx) context.Context {
	if hooked, ok := tx.(*hookedTx); ok {
		ctx = context.WithValue(ctx, txHooksKey{}, hooked.hooks)
		tx = hooked.Tx
	}
	switch t := tx.(type) {
	case *pgxTx:
		return context.WithValue(ctx, txKey{}, t.tx)
	case *sqlTx:
		return context.WithValue(ctx, txKey{}, t.tx)
	}
	return context.WithValue(ctx, txKey{}, tx)
}

// BeginTx starts a new transaction and returns it together with a context
// that has the transaction embedded for use by Exec/Query/QueryRow.
func (db *DB) BeginTx(ctx context.Context) (Tx, context.Context, error) {
	hooks := newTxHooks()
	if db.sqlDB != nil {
		if session := durableSessionFromContext(ctx, db); session != nil && session.sqliteConn != nil {
			if err := session.ensureDurable(ctx); err != nil {
				return nil, ctx, fmt.Errorf("sqlite durable transaction: verify synchronous=FULL: %w", err)
			}
			tx, err := session.sqliteConn.BeginTx(ctx, nil)
			if err != nil {
				return nil, ctx, err
			}
			storTx := &sqlTx{tx: tx}
			txCtx := context.WithValue(context.WithValue(ctx, txKey{}, tx), txHooksKey{}, hooks)
			return &hookedTx{Tx: storTx, hooks: hooks}, txCtx, nil
		}
		tx, err := db.sqlDB.BeginTx(ctx, nil)
		if err != nil {
			return nil, ctx, err
		}
		storTx := &sqlTx{tx: tx}
		txCtx := context.WithValue(context.WithValue(ctx, txKey{}, tx), txHooksKey{}, hooks)
		return &hookedTx{Tx: storTx, hooks: hooks}, txCtx, nil
	}
	if session := durableSessionFromContext(ctx, db); session != nil && session.pgConn != nil {
		if err := session.ensureDurable(ctx); err != nil {
			return nil, ctx, fmt.Errorf("postgres durable transaction: enable synchronous_commit: %w", err)
		}
		tx, err := session.pgConn.Begin(ctx)
		if err != nil {
			return nil, ctx, err
		}
		if err := setPostgresSynchronousCommit(ctx, tx.Exec, "on", true); err != nil {
			return nil, ctx, errors.Join(err, tx.Rollback(ctx))
		}
		storTx := &pgxTx{
			tx: tx,
			beforeCommit: func(commitCtx context.Context) error {
				return verifyPostgresSynchronousCommit(commitCtx, tx.QueryRow)
			},
		}
		txCtx := context.WithValue(context.WithValue(ctx, txKey{}, tx), txHooksKey{}, hooks)
		return &hookedTx{Tx: storTx, hooks: hooks}, txCtx, nil
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return nil, ctx, err
	}
	storTx := &pgxTx{tx: tx}
	txCtx := context.WithValue(context.WithValue(ctx, txKey{}, tx), txHooksKey{}, hooks)
	return &hookedTx{Tx: storTx, hooks: hooks}, txCtx, nil
}

// BeginDurableTx starts a standalone durable transaction. When ctx already
// belongs to a DurableSession it reuses that pinned connection; otherwise the
// returned transaction owns a short-lived session through Commit/Rollback.
func (db *DB) BeginDurableTx(ctx context.Context) (Tx, context.Context, error) {
	if durableSessionFromContext(ctx, db) != nil {
		return db.BeginTx(ctx)
	}
	session, err := db.BeginDurableSession(ctx)
	if err != nil {
		return nil, ctx, err
	}
	tx, txCtx, err := db.BeginTx(session.Context())
	if err != nil {
		return nil, ctx, errors.Join(err, session.Close())
	}
	return &ownedDurableSessionTx{Tx: tx, session: session}, txCtx, nil
}

type ownedDurableSessionTx struct {
	Tx
	session *DurableSession
}

func (tx *ownedDurableSessionTx) Commit(ctx context.Context) error {
	commitErr := tx.Tx.Commit(ctx)
	if commitErr != nil {
		return errors.Join(commitErr, tx.session.Discard(ctx))
	}
	_ = tx.session.Close()
	// Once Commit succeeds, a failure to restore/discard the session must not
	// be exposed as a failed transaction to cross-resource callers.
	return nil
}

func (tx *ownedDurableSessionTx) Rollback(ctx context.Context) error {
	rollbackErr := tx.Tx.Rollback(ctx)
	if rollbackErr != nil {
		return errors.Join(rollbackErr, tx.session.Discard(ctx))
	}
	_ = tx.session.Close()
	return nil
}

func restoreSQLiteSynchronous(ctx context.Context, conn *sql.Conn, mode int) error {
	_, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA synchronous=%d", mode))
	return err
}

// BeginTxForExecution starts a transaction in two phases. Pool acquisition uses
// acquireCtx, so a canceled request or exhausted pool cannot wait forever. The
// SQLite transaction starts with lifetimeCtx because database/sql binds its
// lifetime to that context. PostgreSQL uses acquireCtx for the BEGIN command
// itself, while the returned transaction context and all later operations use
// lifetimeCtx. DSL execution passes a detached context there so request
// cancellation cannot roll an acquired transaction back behind its explicit
// cleanup boundary.
//
// The returned transaction owns the acquired connection and releases it after
// Commit or Rollback. Ordinary callers should keep using BeginTx.
func (db *DB) BeginTxForExecution(acquireCtx, lifetimeCtx context.Context) (Tx, context.Context, error) {
	hooks := newTxHooks()
	if db.sqlDB != nil {
		if session := durableSessionFromContext(lifetimeCtx, db); session != nil && session.sqliteConn != nil {
			if err := session.ensureDurable(lifetimeCtx); err != nil {
				return nil, acquireCtx, err
			}
			tx, err := session.sqliteConn.BeginTx(lifetimeCtx, nil)
			if err != nil {
				return nil, acquireCtx, err
			}
			storTx := &sqlTx{tx: tx}
			txCtx := context.WithValue(context.WithValue(lifetimeCtx, txKey{}, tx), txHooksKey{}, hooks)
			return &hookedTx{Tx: storTx, hooks: hooks}, txCtx, nil
		}
		conn, err := db.sqlDB.Conn(acquireCtx)
		if err != nil {
			return nil, acquireCtx, err
		}
		tx, err := conn.BeginTx(lifetimeCtx, nil)
		if err != nil {
			_ = conn.Close()
			return nil, acquireCtx, err
		}
		storTx := &sqlTx{tx: tx, conn: conn}
		txCtx := context.WithValue(context.WithValue(lifetimeCtx, txKey{}, tx), txHooksKey{}, hooks)
		return &hookedTx{Tx: storTx, hooks: hooks}, txCtx, nil
	}
	if session := durableSessionFromContext(lifetimeCtx, db); session != nil && session.pgConn != nil {
		if err := session.ensureDurable(acquireCtx); err != nil {
			return nil, acquireCtx, err
		}
		tx, err := session.pgConn.Begin(acquireCtx)
		if err != nil {
			return nil, acquireCtx, err
		}
		if err := setPostgresSynchronousCommit(acquireCtx, tx.Exec, "on", true); err != nil {
			return nil, acquireCtx, errors.Join(err, tx.Rollback(acquireCtx))
		}
		storTx := &pgxTx{
			tx: tx,
			beforeCommit: func(commitCtx context.Context) error {
				return verifyPostgresSynchronousCommit(commitCtx, tx.QueryRow)
			},
		}
		txCtx := context.WithValue(context.WithValue(lifetimeCtx, txKey{}, tx), txHooksKey{}, hooks)
		return &hookedTx{Tx: storTx, hooks: hooks}, txCtx, nil
	}

	conn, err := db.pool.Acquire(acquireCtx)
	if err != nil {
		return nil, acquireCtx, err
	}
	tx, err := beginPGTransactionForExecution(conn, acquireCtx)
	if err != nil {
		conn.Release()
		return nil, acquireCtx, err
	}
	storTx := &pgxTx{tx: tx, release: conn.Release}
	txCtx := context.WithValue(context.WithValue(lifetimeCtx, txKey{}, tx), txHooksKey{}, hooks)
	return &hookedTx{Tx: storTx, hooks: hooks}, txCtx, nil
}

// beginPGTransactionForExecution deliberately uses the acquisition context for
// the BEGIN command itself. pgx does not bind transaction lifetime to this
// context, so subsequent operations still use the detached lifetimeCtx stored
// by BeginTxForExecution, while a blocked BEGIN remains cancelable.
func beginPGTransactionForExecution(conn pgExecutionBeginner, acquireCtx context.Context) (pgx.Tx, error) {
	return conn.Begin(acquireCtx)
}

// Exec runs a non-query SQL statement, respecting any transaction in ctx.
func (db *DB) Exec(ctx context.Context, sqlText string, args ...any) (CommandTag, error) {
	if db.sqlDB != nil {
		args = normalizeSQLiteArgs(args)
		if tx, ok := ctx.Value(txKey{}).(*sql.Tx); ok {
			res, err := tx.ExecContext(ctx, sqlText, args...)
			if err != nil {
				return CommandTag{}, err
			}
			n, _ := res.RowsAffected()
			return CommandTag{RowsAffected: n}, nil
		}
		if session := durableSessionFromContext(ctx, db); session != nil && session.sqliteConn != nil {
			if err := session.ensureDurable(ctx); err != nil {
				return CommandTag{}, err
			}
			res, err := session.sqliteConn.ExecContext(ctx, sqlText, args...)
			if err != nil {
				return CommandTag{}, err
			}
			n, _ := res.RowsAffected()
			return CommandTag{RowsAffected: n}, nil
		}
		res, err := db.sqlDB.ExecContext(ctx, sqlText, args...)
		if err != nil {
			return CommandTag{}, err
		}
		n, _ := res.RowsAffected()
		return CommandTag{RowsAffected: n}, nil
	}
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return cmdTag(tx.Exec(ctx, sqlText, args...))
	}
	if session := durableSessionFromContext(ctx, db); session != nil && session.pgConn != nil {
		if err := session.ensureDurable(ctx); err != nil {
			return CommandTag{}, err
		}
		return cmdTag(session.pgConn.Exec(ctx, sqlText, args...))
	}
	return cmdTag(db.pool.Exec(ctx, sqlText, args...))
}

// Query runs a SQL query and returns multiple rows, respecting any transaction in ctx.
func (db *DB) Query(ctx context.Context, sqlText string, args ...any) (Rows, error) {
	if db.sqlDB != nil {
		args = normalizeSQLiteArgs(args)
		if tx, ok := ctx.Value(txKey{}).(*sql.Tx); ok {
			rows, err := tx.QueryContext(ctx, sqlText, args...)
			if err != nil {
				return nil, err
			}
			return &sqlRows{r: rows}, nil
		}
		if session := durableSessionFromContext(ctx, db); session != nil && session.sqliteConn != nil {
			if err := session.ensureDurable(ctx); err != nil {
				return nil, err
			}
			rows, err := session.sqliteConn.QueryContext(ctx, sqlText, args...)
			if err != nil {
				return nil, err
			}
			return &sqlRows{r: rows}, nil
		}
		rows, err := db.sqlDB.QueryContext(ctx, sqlText, args...)
		if err != nil {
			return nil, err
		}
		return &sqlRows{r: rows}, nil
	}
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		rows, err := tx.Query(ctx, sqlText, args...)
		if err != nil {
			return nil, err
		}
		return &pgxRows{r: rows}, nil
	}
	if session := durableSessionFromContext(ctx, db); session != nil && session.pgConn != nil {
		if err := session.ensureDurable(ctx); err != nil {
			return nil, err
		}
		rows, err := session.pgConn.Query(ctx, sqlText, args...)
		if err != nil {
			return nil, err
		}
		return &pgxRows{r: rows}, nil
	}
	rows, err := db.pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	return &pgxRows{r: rows}, nil
}

// QueryRow runs a SQL query expected to return at most one row, respecting any
// transaction in ctx.
func (db *DB) QueryRow(ctx context.Context, sqlText string, args ...any) Row {
	if db.sqlDB != nil {
		args = normalizeSQLiteArgs(args)
		if tx, ok := ctx.Value(txKey{}).(*sql.Tx); ok {
			return sqlRow{r: tx.QueryRowContext(ctx, sqlText, args...)}
		}
		if session := durableSessionFromContext(ctx, db); session != nil && session.sqliteConn != nil {
			if err := session.ensureDurable(ctx); err != nil {
				return errorRow{err: err}
			}
			return sqlRow{r: session.sqliteConn.QueryRowContext(ctx, sqlText, args...)}
		}
		return sqlRow{r: db.sqlDB.QueryRowContext(ctx, sqlText, args...)}
	}
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return pgxRow{r: tx.QueryRow(ctx, sqlText, args...)}
	}
	if session := durableSessionFromContext(ctx, db); session != nil && session.pgConn != nil {
		if err := session.ensureDurable(ctx); err != nil {
			return errorRow{err: err}
		}
		return pgxRow{r: session.pgConn.QueryRow(ctx, sqlText, args...)}
	}
	return pgxRow{r: db.pool.QueryRow(ctx, sqlText, args...)}
}

// exec is the internal helper. Routes through DB.Exec so SQLite works too.
func (db *DB) exec(ctx context.Context, sql string, args ...any) error {
	_, err := db.Exec(ctx, sql, args...)
	return err
}
