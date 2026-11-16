package storage

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

// Exercise public Migrate twice in one transaction while a real PostgreSQL
// waiter is blocked by its transaction-level migration lock.
func TestMigrateBorrowedPostgresAdvisoryLockReentry(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	for _, mode := range []string{"commit", "rollback", "cancel-waiter"} {
		t.Run(mode, func(t *testing.T) {
			ctx, stop := context.WithTimeout(t.Context(), 20*time.Second)
			defer stop()
			schema := NewEphemeralSchemaName()
			db, err := ConnectWithSchema(ctx, dsn, schema)
			require.NoError(t, err)
			defer db.Close()
			require.NoError(t, db.CreateSchema(ctx, schema))
			defer func() { require.NoError(t, db.DropSchemaCascade(context.Background(), schema)) }()
			require.NoError(t, db.EnsureServiceSchema(ctx))
			observer, err := pgx.Connect(ctx, dsn)
			require.NoError(t, err)
			defer func() { require.NoError(t, observer.Close(context.Background())) }()
			e := &metadata.Entity{Name: "MigrationReentry", Kind: metadata.KindCatalog,
				Fields:  []metadata.Field{{ID: "value", Name: "Value", Type: metadata.FieldTypeString}, {ID: "label", Name: "Label", Type: metadata.FieldTypeString}},
				Indexes: []metadata.IndexSpec{{Fields: []string{"Value"}, Unique: true}}}
			require.NoError(t, db.Migrate(ctx, []*metadata.Entity{e}))
			id := uuid.New()
			require.NoError(t, db.Upsert(ctx, e.Name, id, map[string]any{"Value": "preserved", "Label": "shared"}, e))
			var beforeRegistry, beforeIndex string
			require.NoError(t, db.QueryRow(ctx, "SELECT signature FROM _managed_entity_indexes WHERE table_name='migrationreentry'").Scan(&beforeRegistry))
			require.NoError(t, db.QueryRow(ctx, "SELECT indexdef FROM pg_indexes WHERE schemaname=current_schema() AND tablename='migrationreentry' AND indexname <> 'migrationreentry_pkey'").Scan(&beforeIndex))
			waitCtx, cancelWait := context.WithCancel(ctx)
			defer cancelWait()
			done := make(chan error, 1)
			started := false
			rollback := errors.New("requested rollback")
			var key int64
			var txValue string
			txErr := db.WithTx(ctx, func(txCtx context.Context) error {
				if err := db.Migrate(txCtx, []*metadata.Entity{e}); err != nil {
					return err
				}
				var owner int
				if err := db.QueryRow(txCtx, "SELECT pg_backend_pid(), hashtextextended('onebase-migrate-v1|' || current_database() || '|' || current_schema(),0)").Scan(&owner, &key); err != nil {
					return err
				}
				go func() { done <- db.Migrate(waitCtx, []*metadata.Entity{e}) }()
				started = true
				// Observe the blocking PID and exact advisory-lock query. A sleep alone
				// cannot prove that the competing migration has reached the lock.
				deadline := time.Now().Add(3 * time.Second)
				blocked := false
				for time.Now().Before(deadline) {
					err := observer.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
      WHERE $1::int = ANY(pg_blocking_pids(pid)) AND wait_event_type='Lock'
      AND wait_event='advisory' AND query LIKE '%pg_advisory_lock%')`, owner).Scan(&blocked)
					if err != nil {
						return err
					}
					if blocked {
						break
					}
					time.Sleep(time.Millisecond)
				}
				if !blocked {
					return errors.New("competing migration did not enter PostgreSQL advisory-lock wait")
				}
				// Cancel only to break a regression, never to make the second Migrate
				// succeed. This keeps the old implementation from hanging the test.
				watchdog := time.AfterFunc(2*time.Second, cancelWait)
				err := db.Migrate(txCtx, []*metadata.Entity{e})
				watchdog.Stop()
				if waitCtx.Err() != nil {
					return errors.New("reentrant migration required cancellation of its competitor")
				}
				if err != nil {
					return err
				}
				var acquired bool
				if err := observer.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired); err != nil {
					return err
				}
				if acquired {
					_, _ = observer.Exec(ctx, "SELECT pg_advisory_unlock($1)", key)
					return errors.New("borrowed migration released the outer transaction lock")
				}
				if mode == "cancel-waiter" {
					cancelWait()
					select {
					case err := <-done:
						started = false
						if !errors.Is(err, context.Canceled) {
							return errors.New("waiting migration did not return context cancellation")
						}
					case <-ctx.Done():
						return ctx.Err()
					}
					if err := db.Migrate(txCtx, []*metadata.Entity{e}); err != nil {
						return err
					}
				}
				txValue = "committed"
				if mode == "rollback" {
					txValue = "rolled-back"
				}
				if err := db.Upsert(txCtx, e.Name, id, map[string]any{"Value": txValue, "Label": "shared"}, e); err != nil {
					return err
				}
				if mode == "rollback" {
					return rollback
				}
				return nil
			})
			// Always release the outer transaction before joining the competitor.
			// On failure, cancel first so cleanup does not strand a pooled connection.
			if txErr != nil && !errors.Is(txErr, rollback) {
				cancelWait()
			}
			if started {
				select {
				case err := <-done:
					if txErr == nil || errors.Is(txErr, rollback) {
						require.NoError(t, err)
					}
				case <-ctx.Done():
					t.Fatal("competitor did not finish after transaction end")
				}
			}
			if mode == "rollback" {
				require.ErrorIs(t, txErr, rollback)
			} else {
				require.NoError(t, txErr)
			}
			require.Zero(t, db.PoolStats().AcquiredConns, "migration must return all connections")
			var acquired bool
			require.NoError(t, observer.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired))
			require.True(t, acquired, "transaction and session locks must be released")
			_, err = observer.Exec(ctx, "SELECT pg_advisory_unlock($1)", key)
			require.NoError(t, err)
			// A failed index batch must release its session lock too, preserving both
			// the existing managed index and registry. Then normal Migrate still works.
			duplicate := uuid.New()
			require.NoError(t, db.Upsert(ctx, e.Name, duplicate, map[string]any{"Value": "duplicate", "Label": "shared"}, e))
			bad := *e
			bad.Indexes = []metadata.IndexSpec{{Fields: []string{"Value"}, Unique: true}, {Fields: []string{"Label"}, Unique: true}}
			err = db.Migrate(ctx, []*metadata.Entity{&bad})
			var pgErr *pgconn.PgError
			require.ErrorAs(t, err, &pgErr)
			require.Equal(t, "23505", pgErr.Code)
			require.Zero(t, db.PoolStats().AcquiredConns)
			require.NoError(t, observer.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired))
			require.True(t, acquired, "failed migration must release session lock")
			_, err = observer.Exec(ctx, "SELECT pg_advisory_unlock($1)", key)
			require.NoError(t, err)
			var registry, index string
			require.NoError(t, db.QueryRow(ctx, "SELECT signature FROM _managed_entity_indexes WHERE table_name='migrationreentry'").Scan(&registry))
			require.Equal(t, beforeRegistry, registry)
			require.NoError(t, db.QueryRow(ctx, "SELECT indexdef FROM pg_indexes WHERE schemaname=current_schema() AND tablename='migrationreentry' AND indexname <> 'migrationreentry_pkey'").Scan(&index))
			require.Equal(t, beforeIndex, index)
			row, err := db.GetByID(ctx, e.Name, id, e)
			require.NoError(t, err)
			expected := txValue
			if mode == "rollback" {
				expected = "preserved"
			}
			require.Equal(t, expected, row["Value"])
			require.NoError(t, db.Migrate(ctx, []*metadata.Entity{e}))
			require.Zero(t, db.PoolStats().AcquiredConns)
		})
	}
}
