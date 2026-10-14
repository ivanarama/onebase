package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivantit66/onebase/internal/dblock"
)

type entityDDLKey struct{}

func (db *DB) withMigrationLock(ctx context.Context, fn func(context.Context) error) (resultErr error) {
	// A caller's existing transaction already pins its connection. Do not
	// open another one (SQLite has a one-connection pool).
	var session *DurableSession
	if !HasTx(ctx) && durableSessionFromContext(ctx, db) == nil {
		var err error
		session, err = db.BeginDurableSession(ctx)
		if err != nil {
			return err
		}
		ctx = session.Context()
		defer func() { resultErr = errors.Join(resultErr, session.Close()) }()
	}
	// Acquire the connection before migration locks: a borrowed transaction
	// or durable session already owns it and must be able to migrate while
	// another caller is queued for the same one-connection SQLite pool.
	db.migrationMu.Lock()
	defer db.migrationMu.Unlock()
	if db.IsSQLite() && db.databaseFile != "" {
		lease, err := dblock.AcquireSQLiteMigration(db.databaseFile)
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, lease.Close()) }()
	}
	if db.IsPostgres() {
		var key int64
		if err := db.QueryRow(ctx, `SELECT hashtextextended('onebase-migrate-v1|' || current_database() || '|' || current_schema(),0)`).Scan(&key); err != nil {
			return err
		}
		if HasTx(ctx) {
			if _, err := db.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", key); err != nil {
				return err
			}
		} else {
			if _, err := db.Exec(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
				return err
			}
			defer func() {
				cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				var unlocked bool
				err := db.QueryRow(cleanup, "SELECT pg_advisory_unlock($1)", key).Scan(&unlocked)
				if err == nil && !unlocked {
					err = errors.New("migration advisory lock lost")
				}
				resultErr = errors.Join(resultErr, err)
			}()
		}
	}
	return fn(ctx)
}

func (db *DB) checkPreservedEntityIndexes(ctx context.Context, table string, before []EntityIndexDefinition) error {
	after, err := db.readEntityIndexes(ctx, table)
	if err != nil {
		return err
	}
	actual := map[string]string{}
	for _, i := range after {
		actual[i.Name] = i.Fingerprint()
	}
	for _, i := range before {
		if actual[i.Name] != i.Fingerprint() {
			return fmt.Errorf("%s.%s: structural DDL changed an unrelated index; transaction rolled back", table, i.Name)
		}
	}
	return nil
}
