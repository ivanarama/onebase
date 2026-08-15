package storage_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// The register UI resolves reference labels through this exact query shape:
// a batch of UUIDs followed by an RLS predicate. Keep it cross-dialect so the
// PostgreSQL path proves placeholder offsets, UUID arguments and scanned keys,
// rather than merely re-running the SQLite-only unit test in the PG job.
func TestGetFieldsByIDsFilteredMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entity := &metadata.Entity{
			Name: "BulkFields" + uuid.NewString()[:8],
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Label", Type: metadata.FieldTypeString},
				{Name: "Scope", Type: metadata.FieldTypeString},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
		for i, id := range ids {
			scope := "denied"
			if i == 1 {
				scope = "allowed"
			}
			if err := db.Upsert(ctx, entity.Name, id, map[string]any{
				"Label": "row-" + id.String(),
				"Scope": scope,
			}, entity); err != nil {
				t.Fatalf("Upsert row %d: %v", i, err)
			}
		}

		rows, err := db.GetFieldsByIDsFiltered(ctx, entity, ids, entity.Fields[:1], &storage.Predicate{
			Field: "Scope", Op: "eq", Value: "allowed",
		})
		if err != nil {
			t.Fatalf("GetFieldsByIDsFiltered: %v", err)
		}
		if len(rows) != 1 || rows[ids[1].String()]["Label"] != "row-"+ids[1].String() {
			t.Fatalf("filtered rows = %#v, want only %s", rows, ids[1])
		}
		if rows[ids[1].String()]["id"] != ids[1].String() {
			t.Fatalf("scanned id = %v, want canonical %s", rows[ids[1].String()]["id"], ids[1])
		}

		if _, err := db.GetFieldsByIDsFiltered(ctx, entity, ids, entity.Fields[:1], &storage.Predicate{
			Field: "UnknownField", Op: "eq", Value: "x",
		}); err == nil {
			t.Fatal("unknown predicate field must fail closed")
		}
	})
}
