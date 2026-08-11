package storage

import (
	"context"
	"database/sql"
	"sync"
)

// sqlRows wraps sql.Rows into storage.Rows.
type sqlRows struct {
	r *sql.Rows
}

func (s *sqlRows) Next() bool            { return s.r.Next() }
func (s *sqlRows) Scan(dst ...any) error { return s.r.Scan(dst...) }
func (s *sqlRows) Err() error            { return s.r.Err() }
func (s *sqlRows) Close()                { _ = s.r.Close() }
func (s *sqlRows) FieldNames() []string {
	cols, _ := s.r.Columns()
	return cols
}

// sqlRow wraps *sql.Row.
type sqlRow struct {
	r *sql.Row
}

func (s sqlRow) Scan(dst ...any) error { return s.r.Scan(dst...) }

// sqlTx wraps *sql.Tx into storage.Tx.
type sqlTx struct {
	tx   *sql.Tx
	conn *sql.Conn
	done sync.Once
}

func (t *sqlTx) Exec(ctx context.Context, sql string, args ...any) (CommandTag, error) {
	args = normalizeSQLiteArgs(args)
	res, err := t.tx.ExecContext(ctx, sql, args...)
	if err != nil {
		return CommandTag{}, err
	}
	n, _ := res.RowsAffected()
	return CommandTag{RowsAffected: n}, nil
}

func (t *sqlTx) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	args = normalizeSQLiteArgs(args)
	rows, err := t.tx.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &sqlRows{r: rows}, nil
}

func (t *sqlTx) QueryRow(ctx context.Context, sql string, args ...any) Row {
	args = normalizeSQLiteArgs(args)
	return sqlRow{r: t.tx.QueryRowContext(ctx, sql, args...)}
}

func (t *sqlTx) closeConn() {
	if t.conn != nil {
		t.done.Do(func() { _ = t.conn.Close() })
	}
}

func (t *sqlTx) Commit(_ context.Context) error {
	err := t.tx.Commit()
	t.closeConn()
	return err
}

func (t *sqlTx) Rollback(_ context.Context) error {
	err := t.tx.Rollback()
	t.closeConn()
	return err
}
