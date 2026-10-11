package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/jackc/pgx/v5"
)

// Inject a driver iteration failure through DB.Query, while invoking the
// public graph entry points. Query succeeds, one row scans, then Next fails:
// checking only Query/Scan would incorrectly publish a partial page.
type graphFaultRows struct {
	pgx.Rows
	step        int
	closed      bool
	failure     error
	scanFailure error
}

func (r *graphFaultRows) Next() bool { r.step++; return r.step == 1 }
func (r *graphFaultRows) Scan(dst ...any) error {
	if r.scanFailure != nil {
		return r.scanFailure
	}
	*dst[0].(*uuid.UUID) = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	return nil
}
func (r *graphFaultRows) Err() error { return r.failure }
func (r *graphFaultRows) Close()     { r.closed = true }

type graphFaultTx struct {
	pgx.Tx
	rows *graphFaultRows
}

func (tx *graphFaultTx) Query(context.Context, string, ...any) (pgx.Rows, error) { return tx.rows, nil }

func TestReferenceGraphDriverErrorsDiscardPartialPage(t *testing.T) {
	root := &metadata.Entity{Name: "Root", Kind: metadata.KindCatalog}
	doc := &metadata.Entity{Name: "Doc", Kind: metadata.KindDocument, Fields: []metadata.Field{{Name: "Root", RefEntity: root.Name}}}
	src := refsIn{entities: []*metadata.Entity{root, doc}}
	keys, err := ReferenceSources(root.Name, src)
	if err != nil || len(keys) != 1 {
		t.Fatalf("sources: %v %v", keys, err)
	}
	failure := errors.New("connection failed during rows iteration")
	for _, mode := range []string{"rows.Err", "Scan", "iterator"} {
		t.Run(mode, func(t *testing.T) {
			rows := &graphFaultRows{failure: failure}
			if mode == "Scan" {
				rows.failure = nil
				rows.scanFailure = failure
			}
			ctx := context.WithValue(context.Background(), txKey{}, &graphFaultTx{rows: rows})
			db := &DB{dialect: PgDialect{}}
			read := ReferenceRead{Source: keys[0]}
			if mode == "iterator" {
				it, err := db.NewReferenceIterator(root.Name, uuid.New(), src, []ReferenceRead{read}, 1)
				if err != nil {
					t.Fatal(err)
				}
				c, err := it.Next(ctx)
				if !errors.Is(err, failure) || c != nil {
					t.Fatalf("partial candidate escaped: %v %v", c, err)
				}
			} else {
				page, err := db.ReadReferencePage(ctx, root.Name, uuid.New(), src, read, 1, "")
				if !errors.Is(err, failure) || len(page.Keys) != 0 || page.NextCursor != "" {
					t.Fatalf("partial page escaped: %+v %v", page, err)
				}
			}
			if !rows.closed {
				t.Fatal("failed cursor left open")
			}
		})
	}
}
