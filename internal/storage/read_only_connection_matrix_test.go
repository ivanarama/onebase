package storage_test

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

// The same public reader used by CLI preflight must reject accidental writes
// through Exec and Query, and leave the original database readable and unchanged.
func TestReadOnlyConnectionsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		if _, err := db.Exec(ctx, `CREATE TABLE reader_probe (id INTEGER PRIMARY KEY)`); err != nil {
			t.Fatal(err)
		}
		var reader *storage.DB
		var err error
		if db.IsSQLite() {
			reader, err = storage.ConnectSQLiteReadOnly(ctx, db.SQLitePath())
		} else {
			var schema string
			if err := db.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
				t.Fatal(err)
			}
			dsn := os.Getenv("TEST_DATABASE_URL")
			if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
				u, err := url.Parse(dsn)
				if err != nil {
					t.Fatal(err)
				}
				q := u.Query()
				q.Set("search_path", schema)
				u.RawQuery = q.Encode()
				dsn = u.String()
			} else {
				dsn += " search_path=" + schema
			}
			reader, err = storage.ConnectReadOnly(ctx, dsn)
		}
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		var count int
		if err := reader.QueryRow(ctx, `SELECT COUNT(*) FROM reader_probe`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("reader query: %d %v", count, err)
		}
		if _, err := reader.Exec(ctx, `INSERT INTO reader_probe VALUES (1)`); err == nil {
			t.Fatal("read-only Exec accepted a write")
		}
		rows, err := reader.Query(ctx, `INSERT INTO reader_probe VALUES (2) RETURNING id`)
		if err == nil {
			for rows.Next() {
			}
			err = rows.Err()
			rows.Close()
		}
		if err == nil {
			t.Fatal("read-only Query accepted a write")
		}
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM reader_probe`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("reader changed rows: %d %v", count, err)
		}
	})
}
