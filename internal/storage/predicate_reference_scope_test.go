package storage_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestInfoRegReferencePredicateKeepsOuterCorrelationMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ownerEntity := &metadata.Entity{
			Name: "PolicyOwner",
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Name", Type: metadata.FieldTypeString},
				// The referenced table deliberately has the same physical
				// owner_id column as the outer information register. An
				// unqualified correlated column binds to this inner field.
				{Name: "Owner", Type: metadata.FieldType("reference:PolicyOwner"), RefEntity: "PolicyOwner"},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{ownerEntity}); err != nil {
			t.Fatal(err)
		}
		allowedID := uuid.New()
		hiddenID := uuid.New()
		if err := db.Upsert(ctx, ownerEntity.Name, allowedID, map[string]any{
			"Name": "allowed", "Owner": allowedID.String(),
		}, ownerEntity); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, ownerEntity.Name, hiddenID, map[string]any{
			"Name": "hidden", "Owner": nil,
		}, ownerEntity); err != nil {
			t.Fatal(err)
		}

		ir := &metadata.InfoRegister{
			Name: "ReferencePolicyScope",
			Dimensions: []metadata.Field{
				{Name: "Key", Type: metadata.FieldTypeString},
				{Name: "Owner", Type: metadata.FieldType("reference:PolicyOwner"), RefEntity: ownerEntity.Name},
			},
			Resources: []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}},
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		for _, row := range []struct {
			key   string
			owner uuid.UUID
		}{{"allowed", allowedID}, {"hidden", hiddenID}} {
			if err := db.InfoRegSet(ctx, ir,
				map[string]any{"Key": row.key, "Owner": row.owner.String()},
				map[string]any{"Value": row.key}, nil); err != nil {
				t.Fatal(err)
			}
		}

		policy := &storage.Predicate{
			Field:     "Owner",
			RefEntity: ownerEntity,
			RefPredicate: &storage.Predicate{
				Field: "Name", Op: "eq", Value: "allowed",
			},
		}
		rows, err := db.InfoRegList(ctx, ir, storage.RegFilter{RowFilter: policy})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0]["Key"] != "allowed" {
			t.Fatalf("reference policy lost its outer-row correlation: %#v", rows)
		}
		changed, err := db.InfoRegSetIfExistingAllowed(ctx, ir,
			map[string]any{"Key": "hidden", "Owner": hiddenID.String()},
			map[string]any{"Value": "must-not-overwrite"}, nil, policy)
		if err != nil {
			t.Fatal(err)
		}
		if changed {
			t.Fatal("conditional upsert overwrote a row rejected by the reference policy")
		}
		changed, err = db.InfoRegSetIfExistingAllowed(ctx, ir,
			map[string]any{"Key": "allowed", "Owner": allowedID.String()},
			map[string]any{"Value": "updated"}, nil, policy)
		if err != nil {
			t.Fatal(err)
		}
		if !changed {
			t.Fatal("conditional upsert did not update the row admitted by the reference policy")
		}

		deleteByKey := func(key string) []map[string]any {
			t.Helper()
			var deleted []map[string]any
			if err := db.WithTxScope(ctx, func(txCtx context.Context) error {
				var err error
				deleted, err = db.InfoRegDeleteByFilterReturning(txCtx, ir, storage.RegFilter{
					DimValues: map[string]any{"Key": key}, RowFilter: policy,
				})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			return deleted
		}
		if deleted := deleteByKey("hidden"); len(deleted) != 0 {
			t.Fatalf("reference policy deleted a row whose referenced object is hidden: %#v", deleted)
		}
		if deleted := deleteByKey("allowed"); len(deleted) != 1 || deleted[0]["Key"] != "allowed" {
			t.Fatalf("reference policy did not delete the allowed row: %#v", deleted)
		}
		remaining, err := db.InfoRegList(ctx, ir, storage.RegFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(remaining) != 1 || remaining[0]["Key"] != "hidden" {
			t.Fatalf("unexpected remaining rows after filtered deletes: %#v", remaining)
		}
	})
}
