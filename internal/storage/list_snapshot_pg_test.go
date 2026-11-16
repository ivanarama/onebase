package storage

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type listSnapshotTraceKey struct{}

type listSnapshotTrace struct {
	writer  *pgx.Conn
	table   string
	value   any
	once    sync.Once
	err     error
	changed bool
}

func (tr *listSnapshotTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	isList := strings.HasPrefix(data.SQL, "SELECT id, ") && strings.Contains(data.SQL, " FROM "+tr.table)
	return context.WithValue(ctx, listSnapshotTraceKey{}, isList)
}

func (tr *listSnapshotTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if marked, _ := ctx.Value(listSnapshotTraceKey{}).(bool); !marked {
		return
	}
	tr.once.Do(func() {
		// Commit on another connection after List consumes its first SELECT,
		// before any subsequent COUNT or NULL-part SELECT can begin.
		_, tr.err = tr.writer.Exec(ctx, "UPDATE "+tr.table+" SET дата = $1 WHERE id = $2", tr.value, "10000000-0000-0000-0000-000000000000")
		tr.changed = tr.err == nil
	})
}

func TestListConcurrentNullUpdateSnapshot(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	for _, borrowed := range []bool{false, true} {
		for _, toNull := range []bool{false, true} {
			for _, page := range []ListParams{{}, {Limit: 3}, {Limit: 2, Offset: 1}} {
				t.Run(fmt.Sprintf("borrowed=%t/toNull=%t/limit=%d/offset=%d", borrowed, toNull, page.Limit, page.Offset), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					writer, err := pgx.Connect(ctx, dsn)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = writer.Close(context.Background()) })
					schema := NewEphemeralSchemaName()
					if _, err := writer.Exec(ctx, "CREATE SCHEMA "+pgQuoteIdent(schema)); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _, _ = writer.Exec(context.Background(), "DROP SCHEMA "+pgQuoteIdent(schema)+" CASCADE") })
					if _, err := writer.Exec(ctx, "SET search_path TO "+pgQuoteIdent(schema)); err != nil {
						t.Fatal(err)
					}
					entity := &metadata.Entity{Name: "ListSnapshot", Kind: metadata.KindDocument,
						Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}}}
					table := metadata.TableName(entity.Name)
					newer := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
					older := newer.Add(-24 * time.Hour)
					var before, after any = nil, newer
					if toNull {
						before, after = newer, nil
					}
					trace := &listSnapshotTrace{writer: writer, table: table, value: after}
					cfg, err := pgxpool.ParseConfig(dsn)
					if err != nil {
						t.Fatal(err)
					}
					cfg.ConnConfig.RuntimeParams["search_path"] = schema
					cfg.ConnConfig.Tracer = trace
					pool, err := pgxpool.NewWithConfig(ctx, cfg)
					if err != nil {
						t.Fatal(err)
					}
					db := &DB{pool: pool, dialect: PgDialect{}}
					t.Cleanup(db.Close)
					if _, err := db.Exec(ctx, "CREATE TABLE "+table+" (id uuid PRIMARY KEY, дата timestamp, posted boolean, deletion_mark boolean)"); err != nil {
						t.Fatal(err)
					}
					const moving = "10000000-0000-0000-0000-000000000000"
					const fixed = "20000000-0000-0000-0000-000000000000"
					if _, err := db.Exec(ctx, "INSERT INTO "+table+" (id, дата) VALUES ($1, $2), ($3, $4)", moving, before, fixed, older); err != nil {
						t.Fatal(err)
					}
					var rows []map[string]any
					read := func(readCtx context.Context) error {
						var err error
						rows, err = db.List(readCtx, entity.Name, entity, page)
						return err
					}
					if borrowed {
						// Exercise a transaction already used for a query: List must neither
						// change its isolation nor discard its caller's uncommitted writes.
						err = db.WithTx(ctx, func(txCtx context.Context) error {
							if _, err := db.Exec(txCtx, "UPDATE "+table+" SET posted = TRUE WHERE id = $1", fixed); err != nil {
								return err
							}
							if err := read(txCtx); err != nil {
								return err
							}
							var posted bool
							if err := db.QueryRow(txCtx, "SELECT posted FROM "+table+" WHERE id = $1", fixed).Scan(&posted); err != nil {
								return err
							}
							if !posted {
								t.Error("List lost the caller's pending write")
							}
							return nil
						})
					} else {
						err = read(ctx)
					}
					if err != nil {
						t.Fatal(err)
					}
					if trace.err != nil {
						t.Fatal(trace.err)
					}
					if !trace.changed {
						t.Fatal("concurrent UPDATE was not executed")
					}
					want := []string{fixed, moving}
					if toNull {
						want = []string{moving, fixed}
					}
					if page.Limit > 0 {
						want = want[page.Offset:]
					}
					var got []string
					for _, row := range rows {
						got = append(got, fmt.Sprint(row["id"]))
						if fmt.Sprint(row["id"]) == moving && (row["Дата"] == nil) != (before == nil) {
							t.Errorf("List mixed snapshots: date = %v, before = %v", row["Дата"], before)
						}
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("List IDs = %v, want initial snapshot %v", got, want)
					}
					var nowNull bool
					if err := writer.QueryRow(ctx, "SELECT дата IS NULL FROM "+table+" WHERE id = $1", moving).Scan(&nowNull); err != nil {
						t.Fatal(err)
					}
					if nowNull != toNull {
						t.Fatal("concurrent update did not persist")
					}
				})
			}
		}
	}
}
