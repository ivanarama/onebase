package storage_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// A configured is_root attribute predates the pseudo-field. Public list,
// count and membership must keep selecting its value, not parent_id.
func TestChoiceFilterIsRootAttributeMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, hierarchical := range []bool{false, true} {
			t.Run(fmt.Sprint(hierarchical), func(t *testing.T) {
				ctx := context.Background()
				entity := &metadata.Entity{
					Name: fmt.Sprintf("RootAttribute%t", hierarchical), Kind: metadata.KindCatalog,
					Hierarchical: hierarchical,
					Fields:       []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "is_root", Type: metadata.FieldTypeBool}},
				}
				if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
					t.Fatal(err)
				}
				root, child := uuid.New(), uuid.New()
				for _, row := range []struct {
					id    uuid.UUID
					value bool
				}{{root, false}, {child, true}} {
					fields := map[string]any{"Наименование": row.id.String(), "is_root": row.value}
					if hierarchical && row.id == child {
						fields["Родитель"] = root.String()
					}
					if err := db.Upsert(ctx, entity.Name, row.id, fields, entity); err != nil {
						t.Fatal(err)
					}
				}
				for _, value := range []bool{false, true} {
					want, excluded := root, child
					if value {
						want, excluded = child, root
					}
					params := storage.ListParams{ChoicePredicates: []storage.ChoicePredicate{{Field: "IS_ROOT", Op: metadata.FormChoiceOpEqual, Value: value}}}
					rows, err := db.List(ctx, entity.Name, entity, params)
					if err != nil || len(rows) != 1 || fmt.Sprint(rows[0]["id"]) != want.String() {
						t.Fatalf("List is_root=%v: rows=%v err=%v, want %s", value, rows, err, want)
					}
					if count, err := db.CountList(ctx, entity.Name, entity, params); err != nil || count != 1 {
						t.Fatalf("CountList=%d err=%v", count, err)
					}
					for _, id := range []uuid.UUID{want, excluded} {
						if allowed, err := db.ListContainsID(ctx, entity.Name, entity, id, params); err != nil || allowed != (id == want) {
							t.Fatalf("ListContainsID(%s)=%v err=%v", id, allowed, err)
						}
					}
				}
			})
		}
	})
}
