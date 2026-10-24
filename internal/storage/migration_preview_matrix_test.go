package storage_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/dblock"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A preview must never inherit a writable transaction, even when its target is
// another database. Exercise the public openers and commit the outer transaction
// so an escaped write cannot be hidden by test cleanup or an outer rollback.
func TestMigrationPreviewRejectsBorrowedTransactionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, source *storage.DB) {
		path := filepath.Join(t.TempDir(), "preview.db")
		raw, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		_, err = raw.Exec("CREATE TABLE sentinel(value TEXT); INSERT INTO sentinel VALUES ('preserved')")
		require.NoError(t, err)
		require.NoError(t, raw.Close())
		lease, target, err := dblock.AcquireSQLiteTarget(path)
		require.NoError(t, err)
		defer func() { require.NoError(t, lease.Close()) }()
		before, err := os.ReadFile(target)
		require.NoError(t, err)

		targets := []struct {
			name string
			open func(context.Context, func(context.Context, *storage.DB) error) error
		}{
			{"sqlite-other-file", func(ctx context.Context, fn func(context.Context, *storage.DB) error) error {
				return storage.WithSQLiteMigrationPreview(ctx, target, true, fn)
			}},
			{"sqlite-memory", func(ctx context.Context, fn func(context.Context, *storage.DB) error) error {
				return storage.WithSQLiteMigrationPreview(ctx, ":memory:", false, fn)
			}},
			{"postgres", func(ctx context.Context, fn func(context.Context, *storage.DB) error) error {
				return storage.WithPostgresMigrationPreview(ctx, os.Getenv("TEST_DATABASE_URL"), fn)
			}},
		}
		for _, tc := range targets {
			if source.IsPostgres() != (tc.name == "postgres") {
				continue
			}
			t.Run(tc.name, func(t *testing.T) {
				t.Cleanup(func() {
					_, err := source.Exec(context.Background(), "DROP TABLE IF EXISTS preview_write_escape")
					require.NoError(t, err)
					_, err = source.Exec(context.Background(), "DROP TABLE IF EXISTS outer_write_preserved")
					require.NoError(t, err)
				})
				called := false
				err := source.WithTx(t.Context(), func(ctx context.Context) error {
					previewErr := tc.open(ctx, func(ctx context.Context, preview *storage.DB) error {
						called = true
						_, err := preview.Exec(ctx, "CREATE TABLE preview_write_escape(value TEXT)")
						return err
					})
					assert.ErrorContains(t, previewErr, "existing transaction")
					assert.False(t, called, "borrowed context must be rejected before the callback")
					// Rejection must leave the caller's transaction usable.
					_, err := source.Exec(ctx, "CREATE TABLE outer_write_preserved(value TEXT)")
					return err
				})
				require.NoError(t, err)
				var count int
				catalog := "SELECT COUNT(*) FROM sqlite_schema WHERE name='preview_write_escape'"
				if source.IsPostgres() {
					catalog = "SELECT COUNT(*) FROM pg_tables WHERE schemaname=current_schema() AND tablename='preview_write_escape'"
				}
				require.NoError(t, source.QueryRow(t.Context(), catalog).Scan(&count))
				require.Zero(t, count, "preview wrote to the source database")
				_, err = source.Exec(t.Context(), "INSERT INTO outer_write_preserved VALUES ('committed')")
				require.NoError(t, err)
			})
		}
		after, err := os.ReadFile(target)
		require.NoError(t, err)
		require.Equal(t, before, after, "preview target changed")
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			_, err := os.Stat(target + suffix)
			require.True(t, os.IsNotExist(err), "preview created sidecar %s: %v", suffix, err)
		}
	})
}
