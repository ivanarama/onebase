package storage

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// pgxRows wraps pgx.Rows into storage.Rows.
type pgxRows struct {
	r pgx.Rows
}

func (p *pgxRows) Next() bool            { return p.r.Next() }
func (p *pgxRows) Scan(dst ...any) error { return p.r.Scan(dst...) }
func (p *pgxRows) Err() error            { return p.r.Err() }
func (p *pgxRows) Close()                { p.r.Close() }
func (p *pgxRows) FieldNames() []string {
	fds := p.r.FieldDescriptions()
	out := make([]string, len(fds))
	for i, fd := range fds {
		out[i] = string(fd.Name)
	}
	return out
}

// pgxRow wraps pgx.Row into storage.Row.
type pgxRow struct {
	r pgx.Row
}

func (p pgxRow) Scan(dst ...any) error { return p.r.Scan(dst...) }

func cmdTag(tag pgconn.CommandTag, err error) (CommandTag, error) {
	return CommandTag{RowsAffected: tag.RowsAffected()}, err
}

// pgxTx wraps pgx.Tx into storage.Tx.
type pgxTx struct {
	tx      pgx.Tx
	release func()
	done    sync.Once
}

func (t *pgxTx) releaseConn() {
	if t.release != nil {
		t.done.Do(t.release)
	}
}

func (t *pgxTx) Exec(ctx context.Context, sql string, args ...any) (CommandTag, error) {
	return cmdTag(t.tx.Exec(ctx, sql, args...))
}

func (t *pgxTx) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	rows, err := t.tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &pgxRows{r: rows}, nil
}

func (t *pgxTx) QueryRow(ctx context.Context, sql string, args ...any) Row {
	return pgxRow{r: t.tx.QueryRow(ctx, sql, args...)}
}

func (t *pgxTx) Commit(ctx context.Context) error {
	err := t.tx.Commit(ctx)
	// pgx closes dbTx on every Commit outcome (and kills an indeterminate
	// connection itself), so the acquired pool slot must always be released.
	t.releaseConn()
	return err
}

func (t *pgxTx) Rollback(ctx context.Context) error {
	err := t.tx.Rollback(ctx)
	t.releaseConn()
	return err
}
