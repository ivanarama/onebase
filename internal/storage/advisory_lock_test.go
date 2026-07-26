package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAdvisoryXactLock_SQLiteNoop(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.AdvisoryXactLock(ctx, []string{"reg|item=1"}); err != nil {
		t.Fatalf("sqlite advisory lock should be noop: %v", err)
	}
}

func TestAdvisoryLockKeyStable(t *testing.T) {
	first := advisoryLockKey("A")
	second := advisoryLockKey("A")
	if first != second {
		t.Fatal("advisoryLockKey must be stable")
	}
	if first == advisoryLockKey("B") {
		t.Fatal("different keys should not collide in this smoke test")
	}
}
