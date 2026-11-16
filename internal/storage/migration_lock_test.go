package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/stretchr/testify/require"
)

func TestMigrateBorrowedConnectionBeforeMigrationLock(t *testing.T) {
	for _, borrowed := range []string{"transaction", "durable-session"} {
		t.Run(borrowed, func(t *testing.T) {
			db, err := ConnectSQLite(t.Context(), filepath.Join(t.TempDir(), "migration.db"))
			require.NoError(t, err)
			t.Cleanup(db.Close)
			e := &metadata.Entity{Name: "ConcurrentMigration", Kind: metadata.KindCatalog}
			require.NoError(t, db.Migrate(t.Context(), []*metadata.Entity{e}))

			compete := func(borrowedCtx context.Context) error {
				if err := db.Migrate(borrowedCtx, []*metadata.Entity{e}); err != nil {
					return err
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				t.Cleanup(cancel)
				done := make(chan error, 1)
				waits := db.sqlDB.Stats().WaitCount
				go func() { done <- db.Migrate(ctx, []*metadata.Entity{e}) }()
				// The competing public Migrate must be waiting for the connection
				// held by the callback, rather than merely not scheduled yet.
				deadline := time.Now().Add(2 * time.Second)
				for db.sqlDB.Stats().WaitCount == waits && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if db.sqlDB.Stats().WaitCount == waits {
					cancel()
					<-done
					t.Error("competing migration did not enter the connection queue")
					return context.DeadlineExceeded
				}
				// Break the old lock inversion so a regression fails without
				// leaving either goroutine or the owning transaction stuck.
				watchdog := time.AfterFunc(2*time.Second, cancel)
				err := db.Migrate(borrowedCtx, []*metadata.Entity{e})
				watchdog.Stop()
				if ctx.Err() != nil {
					t.Error("borrowed migration waited for cancellation of its competitor")
				}
				// The competitor should finish after the borrowed connection is
				// released. Check it outside the owning callback.
				t.Cleanup(func() { require.NoError(t, <-done) })
				return err
			}

			if borrowed == "transaction" {
				require.NoError(t, db.WithTx(t.Context(), compete))
			} else {
				session, err := db.BeginDurableSession(t.Context())
				require.NoError(t, err)
				err = compete(session.Context())
				require.NoError(t, session.Close())
				require.NoError(t, err)
			}
		})
	}
}
