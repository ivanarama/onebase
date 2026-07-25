package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestWithTxScopeRollsBackOnlyBorrowedScope(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "scope.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(ctx, `CREATE TABLE scoped_writes (value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	var hooks []string
	err = db.WithTx(ctx, func(outerCtx context.Context) error {
		if _, err := db.Exec(outerCtx, `INSERT INTO scoped_writes(value) VALUES (?)`, "outer"); err != nil {
			return err
		}
		innerErr := db.WithTxScope(outerCtx, func(innerCtx context.Context) error {
			if _, err := db.Exec(innerCtx, `INSERT INTO scoped_writes(value) VALUES (?)`, "inner"); err != nil {
				return err
			}
			DeferUntilTxCommit(innerCtx, func() { hooks = append(hooks, "inner commit") })
			DeferUntilTxRollback(innerCtx, func() { hooks = append(hooks, "inner rollback") })
			return errors.New("reject inner write")
		})
		if innerErr == nil {
			return errors.New("inner scope unexpectedly succeeded")
		}

		var count int
		if err := db.QueryRow(outerCtx, `SELECT COUNT(*) FROM scoped_writes`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return errors.New("inner row survived savepoint rollback")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var values []string
	rows, err := db.Query(ctx, `SELECT value FROM scoped_writes`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if len(values) != 1 || values[0] != "outer" {
		t.Fatalf("committed rows = %v, want [outer]", values)
	}
	if len(hooks) != 1 || hooks[0] != "inner rollback" {
		t.Fatalf("savepoint hooks = %v, want [inner rollback]", hooks)
	}
}
