//go:build integration

package storage

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func connectPGForAdvisoryLock(t *testing.T) *DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

func TestAdvisoryXactLock_PostgresBlocksUntilTxEnd(t *testing.T) {
	db1 := connectPGForAdvisoryLock(t)
	db2 := connectPGForAdvisoryLock(t)
	ctx := context.Background()

	tx1, txCtx1, err := db1.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx1.Rollback(ctx)
	if err := db1.AdvisoryXactLock(txCtx1, []string{"reg|item=1"}); err != nil {
		t.Fatalf("first lock: %v", err)
	}

	lockCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	tx2, txCtx2, err := db2.BeginTx(lockCtx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx2.Rollback(context.Background())
	if err := db2.AdvisoryXactLock(txCtx2, []string{"reg|item=1"}); err == nil {
		t.Fatal("second lock should block until context deadline")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock err = %v, want deadline", err)
	}

	if err := tx1.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	tx3, txCtx3, err := db2.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx3.Rollback(ctx)
	if err := db2.AdvisoryXactLock(txCtx3, []string{"reg|item=1"}); err != nil {
		t.Fatalf("lock after first tx rollback: %v", err)
	}
}
