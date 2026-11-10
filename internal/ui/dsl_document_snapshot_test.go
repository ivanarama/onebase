package ui

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/jackc/pgx/v5"
)

// Pause an actual header SELECT after its statement snapshot was taken. A
// concurrent commit must leave this reader with the old CAS token as well as
// the old fields; querying the version again would authorize a lost update.
func TestDocumentObjectSnapshotKeepsHeaderVersion(t *testing.T) {
	if err := registerTableCardSnapshotProbe(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"get-object", "read"} {
		t.Run(mode, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				s, doc := writePostServer(t, db, "")
				id := seedWritePost(t, db, doc)
				proxy := &docProxy{s: s, entity: doc}
				var w *docWriter
				if mode == "read" {
					v, err := proxy.LoadObject(id.String())
					if err != nil {
						t.Fatal(err)
					}
					w = v.(*docWriter)
				}
				key := uuid.NewString()
				gate := &tableCardSnapshotGate{ctx: ctx, entered: make(chan struct{}), release: make(chan struct{})}
				tableCardSQLiteGates.Store(key, gate)
				defer tableCardSQLiteGates.Delete(key)
				var once sync.Once
				unblock := func() { once.Do(func() { close(gate.release) }) }
				defer unblock()
				var owner *pgx.Conn
				lockID := uuid.New()
				lockKey := int64(binary.BigEndian.Uint32(lockID[:4])) + 1
				if db.IsPostgres() {
					var err error
					owner, err = pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
					if err != nil {
						t.Fatal(err)
					}
					defer owner.Close(context.Background())
					if _, err = owner.Exec(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
						t.Fatal(err)
					}
					defer owner.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", lockKey)
					_, err = db.Exec(ctx, fmt.Sprintf(`CREATE FUNCTION onebase_doc_snapshot_probe(v bigint) RETURNS bigint LANGUAGE plpgsql VOLATILE AS $$ BEGIN PERFORM pg_advisory_xact_lock(%d); RETURN v; END $$`, lockKey))
					if err != nil {
						t.Fatal(err)
					}
				}
				table := metadata.TableName(doc.Name)
				rows, err := db.Query(ctx, "SELECT * FROM "+table+" LIMIT 0")
				if err != nil {
					t.Fatal(err)
				}
				columns := rows.FieldNames()
				rows.Close()
				projection := make([]string, len(columns))
				for i, column := range columns {
					quoted := pgx.Identifier{column}.Sanitize()
					projection[i] = quoted
					if column == "_version" {
						if db.IsPostgres() {
							projection[i] = "onebase_doc_snapshot_probe(" + quoted + ") AS " + quoted
						} else {
							projection[i] = "onebase_card_snapshot_probe(" + quoted + ", '" + key + "') AS " + quoted
						}
					}
				}
				if _, err = db.Exec(ctx, "ALTER TABLE "+table+" RENAME TO onebase_doc_snapshot_backing"); err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(ctx, "CREATE VIEW "+table+" AS SELECT "+strings.Join(projection, ", ")+" FROM onebase_doc_snapshot_backing"); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() {
					if mode == "read" {
						done <- w.read()
						return
					}
					v, e := proxy.LoadObject(id.String())
					if e == nil {
						w = v.(*docWriter)
					}
					done <- e
				}()
				if db.IsPostgres() {
					for {
						var blocked bool
						if err := owner.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=$1 AND NOT granted)", lockKey).Scan(&blocked); err != nil {
							t.Fatal(err)
						}
						if blocked {
							break
						}
						select {
						case <-ctx.Done():
							t.Fatal(ctx.Err())
						case <-time.After(5 * time.Millisecond):
						}
					}
				} else {
					select {
					case <-gate.entered:
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
				}
				writer := tableCardConcurrentDB(t, db)
				if _, err := writer.Exec(ctx, "UPDATE onebase_doc_snapshot_backing SET ввод='concurrent-input',_version=_version+1"); err != nil {
					t.Fatal(err)
				}
				if db.IsPostgres() {
					if _, err := owner.Exec(ctx, "SELECT pg_advisory_unlock($1)", lockKey); err != nil {
						t.Fatal(err)
					}
				} else {
					unblock()
				}
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				if w.expectedVersion == nil || *w.expectedVersion != 1 {
					t.Fatalf("stale fields paired with version %v, want 1", w.expectedVersion)
				}
				if w.obj.Fields["ввод"] == "concurrent-input" {
					t.Fatal("barrier did not capture the old header")
				}
				if _, err := db.Exec(ctx, "DROP VIEW "+table); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(ctx, "ALTER TABLE onebase_doc_snapshot_backing RENAME TO "+table); err != nil {
					t.Fatal(err)
				}
				w.Set("След", "receipt")
				if err := w.write(); err == nil || !strings.Contains(err.Error(), "объект изменён другим пользователем") {
					t.Fatalf("stale save: %v", err)
				}
				row, err := db.GetByID(ctx, doc.Name, id, doc)
				if err != nil || row["Ввод"] != "concurrent-input" {
					t.Fatalf("concurrent input lost: %v %v", row, err)
				}
			})
		})
	}
}
