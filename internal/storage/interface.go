package storage

import "context"

// CommandTag is the result of a non-query SQL command (Exec). Currently
// reports RowsAffected; matches pgconn.CommandTag.RowsAffected().
type CommandTag struct {
	RowsAffected int64
}

// Row is a one-row query result. Matches pgx.Row / sql.Row.
type Row interface {
	Scan(dst ...any) error
}

type errorRow struct{ err error }

func (row errorRow) Scan(...any) error { return row.err }

// Rows is a multi-row query result. Matches pgx.Rows / sql.Rows.
type Rows interface {
	Next() bool
	Scan(dst ...any) error
	Err() error
	Close()
	// FieldNames returns the column names of the result set. For pgx this is
	// derived from FieldDescriptions; for database/sql it's sql.Rows.Columns().
	FieldNames() []string
}

// Tx is a database transaction abstraction independent of the underlying
// driver (pgx / database/sql). Use db.BeginTx to obtain one.
type Tx interface {
	Exec(ctx context.Context, sql string, args ...any) (CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) Row
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}
