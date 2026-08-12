package storage

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestDBCloseHooksRunOnceInReverseOrder(t *testing.T) {
	db, err := ConnectSQLite(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	var got []int
	db.AddCloseHook(func() error { got = append(got, 1); return nil })
	db.AddCloseHook(func() error { got = append(got, 2); return nil })
	db.Close()
	db.Close()
	if len(got) != 2 || got[0] != 2 || got[1] != 1 {
		t.Fatalf("close hook order = %v, want [2 1]", got)
	}
}

func TestDBCloseHookAddedAfterCloseRunsImmediately(t *testing.T) {
	db, err := ConnectSQLite(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	var calls atomic.Int32
	db.AddCloseHook(func() error { calls.Add(1); return nil })
	if calls.Load() != 1 {
		t.Fatalf("late hook calls = %d, want 1", calls.Load())
	}
}
