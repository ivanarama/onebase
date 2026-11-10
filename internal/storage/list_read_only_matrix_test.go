package storage_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestListDefaultOrderReadOnlyContext(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		cat := &metadata.Entity{Name: "ReadOnlyCatalog", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
		doc := &metadata.Entity{Name: "ReadOnlyDocument", Kind: metadata.KindDocument,
			Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}}}
		if err := db.Migrate(ctx, []*metadata.Entity{cat, doc}); err != nil {
			t.Fatal(err)
		}
		ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
		date := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		for i, id := range ids {
			names := []any{"B", nil, "A"}
			dates := []any{date, nil, date.Add(time.Hour)}
			if err := db.Upsert(ctx, cat.Name, id, map[string]any{"Наименование": names[i]}, cat); err != nil {
				t.Fatal(err)
			}
			if err := db.Upsert(ctx, doc.Name, id, map[string]any{"Дата": dates[i]}, doc); err != nil {
				t.Fatal(err)
			}
		}
		check := func(readCtx context.Context) error {
			for _, ent := range []*metadata.Entity{cat, doc} {
				for _, page := range []storage.ListParams{{}, {Limit: 1}, {Limit: 2, Offset: 1}, {Limit: 2, Offset: 3}} {
					rows, err := db.List(readCtx, ent.Name, ent, page)
					if err != nil {
						return fmt.Errorf("%s page %+v: %w", ent.Name, page, err)
					}
					want := []string{ids[2].String(), ids[0].String(), ids[1].String()}
					if page.Limit > 0 {
						want = want[page.Offset:]
						if len(want) > page.Limit {
							want = want[:page.Limit]
						}
					}
					got := make([]string, 0, len(rows))
					for _, row := range rows {
						got = append(got, fmt.Sprint(row["id"]))
					}
					if !reflect.DeepEqual(got, want) {
						return fmt.Errorf("%s page %+v: IDs %v, want %v", ent.Name, page, got, want)
					}
				}
			}
			// Snapshot setup must not grant the caller access to arbitrary Exec
			// statements or writes through either query entry point.
			if _, err := db.Exec(readCtx, "SET TRANSACTION READ WRITE"); !errors.Is(err, storage.ErrReadOnlyContext) {
				return fmt.Errorf("Exec transaction setting: %v", err)
			}
			q := "DELETE FROM readonlydocument RETURNING id"
			if _, err := db.Exec(readCtx, q); !errors.Is(err, storage.ErrReadOnlyContext) {
				return fmt.Errorf("Exec write: %v", err)
			}
			if _, err := db.Query(readCtx, q); !errors.Is(err, storage.ErrReadOnlyContext) {
				return fmt.Errorf("Query write: %v", err)
			}
			var id string
			if err := db.QueryRow(readCtx, q).Scan(&id); !errors.Is(err, storage.ErrReadOnlyContext) {
				return fmt.Errorf("QueryRow write: %v", err)
			}
			var count int
			if err := db.QueryRow(readCtx, "SELECT COUNT(*) FROM readonlydocument").Scan(&count); err != nil || count != len(ids) {
				return fmt.Errorf("read after rejected writes: count=%d, err=%v", count, err)
			}
			return nil
		}
		t.Run("own_transactions", func(t *testing.T) {
			if err := check(storage.ReadOnlyContext(ctx)); err != nil {
				t.Fatal(err)
			}
		})
		t.Run("borrowed_transaction", func(t *testing.T) {
			if err := db.WithTx(ctx, func(txCtx context.Context) error {
				return check(storage.ReadOnlyContext(txCtx))
			}); err != nil {
				t.Fatal(err)
			}
		})
		t.Run("read_snapshot", func(t *testing.T) {
			if err := db.WithReadSnapshot(storage.ReadOnlyContext(ctx), check); err != nil {
				t.Fatal(err)
			}
		})
	})
}
