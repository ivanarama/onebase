package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
)

type txKey struct{}

var txScopeSequence atomic.Uint64

// IsNotFound reports the portable no-row condition for both storage drivers.
func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows)
}

// HasTx reports whether ctx already carries an active storage transaction.
func HasTx(ctx context.Context) bool {
	return ctx.Value(txKey{}) != nil
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
		tx, err := db.sqlDB.BeginTx(ctx, nil)
		if err != nil {
			return nil, ctx, err
		}
		storTx := &sqlTx{tx: tx}
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

// BeginTxForExecution starts a transaction in two phases. Pool acquisition uses
// acquireCtx, so a canceled request or exhausted pool cannot wait forever. Once
// a connection has been acquired, the transaction itself is bound to
// lifetimeCtx; DSL execution passes a detached context here so request
// cancellation cannot make database/sql roll the transaction back behind its
// explicit cleanup boundary.
//
// The returned transaction owns the acquired connection and releases it after
// Commit or Rollback. Ordinary callers should keep using BeginTx.
func (db *DB) BeginTxForExecution(acquireCtx, lifetimeCtx context.Context) (Tx, context.Context, error) {
	hooks := newTxHooks()
	if db.sqlDB != nil {
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

	conn, err := db.pool.Acquire(acquireCtx)
	if err != nil {
		return nil, acquireCtx, err
	}
	tx, err := conn.Begin(lifetimeCtx)
	if err != nil {
		conn.Release()
		return nil, acquireCtx, err
	}
	storTx := &pgxTx{tx: tx, release: conn.Release}
	txCtx := context.WithValue(context.WithValue(lifetimeCtx, txKey{}, tx), txHooksKey{}, hooks)
	return &hookedTx{Tx: storTx, hooks: hooks}, txCtx, nil
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
		return sqlRow{r: db.sqlDB.QueryRowContext(ctx, sqlText, args...)}
	}
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return pgxRow{r: tx.QueryRow(ctx, sqlText, args...)}
	}
	return pgxRow{r: db.pool.QueryRow(ctx, sqlText, args...)}
}

// exec is the internal helper. Routes through DB.Exec so SQLite works too.
func (db *DB) exec(ctx context.Context, sql string, args ...any) error {
	_, err := db.Exec(ctx, sql, args...)
	return err
}
