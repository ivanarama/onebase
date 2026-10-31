package storage_test

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
	"testing"
)

func TestListProjectionPublicMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		for _, kind := range []metadata.Kind{metadata.KindCatalog, metadata.KindDocument} {
			t.Run(string(kind), func(t *testing.T) {
				e := &metadata.Entity{Name: "ListProjection" + string(kind), Kind: kind, Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}, {Name: "Amount", Type: metadata.FieldTypeNumber}, {Name: "Flag", Type: metadata.FieldTypeBool}, {Name: "Owner", Type: metadata.FieldTypeString}}}
				if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
					t.Fatal(err)
				}
				for i, amount := range []any{decimal.NewFromInt(10), decimal.NewFromInt(2), decimal.NewFromInt(2), nil, decimal.Zero} {
					id := uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", 5-i))
					if err := db.Upsert(ctx, e.Name, id, map[string]any{"Name": fmt.Sprint(i), "Amount": amount, "Flag": false, "Owner": "alice"}, e); err != nil {
						t.Fatal(err)
					}
				}
				if err := db.Upsert(ctx, e.Name, uuid.MustParse("00000000-0000-0000-0000-000000000006"), map[string]any{"Name": "forbidden", "Amount": decimal.NewFromInt(-1), "Flag": false, "Owner": "bob"}, e); err != nil {
					t.Fatal(err)
				}
				p := storage.ListParams{Equalities: []storage.ListEquality{{Field: "Flag", Value: false}}, OrderKeys: []storage.ListOrderKey{{Field: "Amount"}}, RowFilterEvaluated: true, RowFilter: &storage.Predicate{Field: "Owner", Value: "alice"}, Limit: 4}
				rows, err := db.List(ctx, e.Name, e, p)
				if err != nil {
					t.Fatal(err)
				}
				want := []string{"4", "2", "1", "0"}
				if len(rows) != len(want) {
					t.Fatalf("rows=%v", rows)
				}
				for i, w := range want {
					if rows[i]["Name"] != w {
						t.Fatalf("numeric/null/tie/RLS order=%v", rows)
					}
				}
				p.Equalities = append(p.Equalities, storage.ListEquality{Field: "Amount", Value: decimal.Zero})
				rows, err = db.List(ctx, e.Name, e, p)
				if err != nil || len(rows) != 1 || rows[0]["Name"] != "4" {
					t.Fatalf("zero/false AND=%v %v", rows, err)
				}
				p.Equalities[1].Value = decimal.RequireFromString("2.00")
				rows, err = db.List(ctx, e.Name, e, p)
				if err != nil || len(rows) != 2 {
					t.Fatalf("numeric equality=%v %v", rows, err)
				}
				p.Equalities[1].Value = nil
				rows, err = db.List(ctx, e.Name, e, p)
				if err != nil || len(rows) != 1 || rows[0]["Name"] != "3" {
					t.Fatalf("NULL equality=%v %v", rows, err)
				}
				p.Equalities = []storage.ListEquality{{Field: "Name; DROP TABLE x", Value: "x"}}
				if _, err := db.List(ctx, e.Name, e, p); err == nil {
					t.Fatal("untrusted field accepted")
				}
			})
		}
	})
}
