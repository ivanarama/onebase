package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ListSQLCaptureForTest observes the actual driver boundary. It neither builds
// List SQL nor replaces results; external dialect-matrix tests still call List.
// This file is linked only into the test binary.
type ListSQLCaptureForTest struct {
	mu      sync.Mutex
	queries []struct {
		SQL  string
		Args []any
	}
}

func (r *ListSQLCaptureForTest) record(query string, args []any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queries = append(r.queries, struct {
		SQL  string
		Args []any
	}{query, append([]any(nil), args...)})
}

func (r *ListSQLCaptureForTest) Queries() []struct {
	SQL  string
	Args []any
} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]struct {
		SQL  string
		Args []any
	}(nil), r.queries...)
}

func (r *ListSQLCaptureForTest) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	r.record(data.SQL, data.Args)
	return ctx
}
func (*ListSQLCaptureForTest) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

type listSQLConnector struct {
	driver  driver.Driver
	dsn     string
	capture *ListSQLCaptureForTest
}

func (c *listSQLConnector) Driver() driver.Driver { return c.driver }
func (c *listSQLConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &listSQLConn{Conn: conn, capture: c.capture}, nil
}

type listSQLConn struct {
	driver.Conn
	capture *ListSQLCaptureForTest
}

func (c *listSQLConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, fmt.Errorf("test SQLite driver lacks QueryerContext")
	}
	values := make([]any, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	c.capture.record(query, values)
	return q.QueryContext(ctx, query, args)
}
func (c *listSQLConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	q, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return q.ExecContext(ctx, query, args)
}

func InstallListSQLCaptureForTest(t *testing.T, db *DB) *ListSQLCaptureForTest {
	t.Helper()
	capture := &ListSQLCaptureForTest{}
	if db.sqlDB != nil {
		original := db.sqlDB
		// Keep the real matrix database and schema, using a separate connection
		// only so database/sql can intercept QueryContext without production hooks.
		dsn := schemaRevisionSQLiteURI(db.databaseFile, "mode=rw") + "&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)"
		observed := sql.OpenDB(&listSQLConnector{driver: original.Driver(), dsn: dsn, capture: capture})
		observed.SetMaxOpenConns(1)
		if err := observed.PingContext(context.Background()); err != nil {
			_ = observed.Close()
			t.Fatal(err)
		}
		db.sqlDB = observed
		t.Cleanup(func() {
			db.sqlDB = original
			if err := observed.Close(); err != nil {
				t.Error(err)
			}
		})
	} else {
		original := db.pool
		cfg := original.Config()
		if cfg.ConnConfig.Tracer != nil {
			t.Fatal("matrix pool unexpectedly has a query tracer")
		}
		cfg.ConnConfig.Tracer = capture
		observed, err := pgxpool.NewWithConfig(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		db.pool = observed
		t.Cleanup(func() { db.pool = original; observed.Close() })
	}
	return capture
}
