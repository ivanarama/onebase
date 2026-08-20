package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

type requiredValueRef struct{ id string }

func (r *requiredValueRef) GetRefUUID() string { return r.id }

func requiredPersistenceEntities() (*metadata.Entity, *metadata.Entity) {
	owner := &metadata.Entity{
		Name: "RequiredOwner",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Label", Type: metadata.FieldTypeString},
		},
	}
	item := &metadata.Entity{
		Name: "RequiredPersistenceItem",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Name", Type: metadata.FieldTypeString, Required: true},
			{
				Name:      "Owner",
				Type:      metadata.FieldType("reference:RequiredOwner"),
				RefEntity: owner.Name,
				Required:  true,
			},
			{Name: "Note", Type: metadata.FieldTypeString},
		},
		TableParts: []metadata.TablePart{
			{
				Name: "Lines",
				Fields: []metadata.Field{
					{Name: "Product", Type: metadata.FieldTypeString, Required: true},
					{
						Name:      "Owner",
						Type:      metadata.FieldType("reference:RequiredOwner"),
						RefEntity: owner.Name,
						Required:  true,
					},
				},
			},
		},
	}
	return owner, item
}

func TestRequiredPersistence_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ownerEntity, item := requiredPersistenceEntities()
		if err := db.EnsureAuditSchema(ctx); err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(ctx, []*metadata.Entity{ownerEntity, item}); err != nil {
			t.Fatal(err)
		}

		ownerID := uuid.New()
		if err := db.Upsert(ctx, ownerEntity.Name, ownerID,
			map[string]any{"Label": "primary"}, ownerEntity); err != nil {
			t.Fatalf("seed reference target: %v", err)
		}

		t.Run("direct create requires the complete object", func(t *testing.T) {
			id := uuid.New()
			err := db.Upsert(ctx, item.Name, id,
				map[string]any{"Owner": ownerID.String()}, item)
			if !errors.Is(err, storage.ErrRequiredFieldEmpty) {
				t.Fatalf("direct create without Name: got %v, want ErrRequiredFieldEmpty", err)
			}
			if _, err := db.GetByID(ctx, item.Name, id, item); !storage.IsNotFound(err) {
				t.Fatalf("rejected direct create persisted a row: %v", err)
			}
		})

		t.Run("partial upsert preserves omitted required values", func(t *testing.T) {
			id := uuid.New()
			seedRequiredItem(t, ctx, db, item, id, ownerID, "kept", "before")

			if err := db.Upsert(ctx, item.Name, id,
				map[string]any{"Note": "after"}, item); err != nil {
				t.Fatalf("partial Upsert: %v", err)
			}

			got := loadRequiredItem(t, ctx, db, item, id)
			assertRequiredItem(t, got, "kept", ownerID, "after")
			if got["_version"] != int64(2) {
				t.Fatalf("partial Upsert version = %#v, want int64(2)", got["_version"])
			}
			entries, err := db.AuditByRecord(ctx, item.Name, id)
			if err != nil {
				t.Fatalf("reload audit: %v", err)
			}
			var updateFields []string
			for _, entry := range entries {
				if entry.Action == "update" {
					updateFields = append(updateFields, entry.Field)
				}
			}
			if len(updateFields) != 1 || updateFields[0] != "Note" {
				t.Fatalf("partial update audit fields = %#v, want only Note", updateFields)
			}
		})

		t.Run("versioned partial update preserves values and stale CAS wins", func(t *testing.T) {
			id := uuid.New()
			seedRequiredItem(t, ctx, db, item, id, ownerID, "cas-kept", "before")

			expected := int64(1)
			if err := db.UpsertVersioned(ctx, item.Name, id,
				map[string]any{"Note": "after"}, item, &expected); err != nil {
				t.Fatalf("partial UpsertVersioned: %v", err)
			}
			got := loadRequiredItem(t, ctx, db, item, id)
			assertRequiredItem(t, got, "cas-kept", ownerID, "after")
			if got["_version"] != int64(2) {
				t.Fatalf("partial UpsertVersioned version = %#v, want int64(2)", got["_version"])
			}

			err := db.UpsertVersioned(ctx, item.Name, id,
				map[string]any{"Name": "   "}, item, &expected)
			if !errors.Is(err, storage.ErrVersionConflict) {
				t.Fatalf("stale CAS with invalid required value: got %v, want ErrVersionConflict", err)
			}
			got = loadRequiredItem(t, ctx, db, item, id)
			assertRequiredItem(t, got, "cas-kept", ownerID, "after")
			if got["_version"] != int64(2) {
				t.Fatalf("rejected stale write changed version to %#v", got["_version"])
			}

			current := int64(2)
			err = db.UpsertVersioned(ctx, item.Name, id,
				map[string]any{"Name": "   "}, item, &current)
			if !errors.Is(err, storage.ErrRequiredFieldEmpty) {
				t.Fatalf("current CAS with invalid required value: got %v, want ErrRequiredFieldEmpty", err)
			}
			got = loadRequiredItem(t, ctx, db, item, id)
			assertRequiredItem(t, got, "cas-kept", ownerID, "after")
			if got["_version"] != int64(2) {
				t.Fatalf("required rejection changed version to %#v", got["_version"])
			}
			entries, err := db.AuditByRecord(ctx, item.Name, id)
			if err != nil {
				t.Fatalf("reload audit: %v", err)
			}
			var updates int
			for _, entry := range entries {
				if entry.Action == "update" {
					updates++
				}
			}
			if updates != 1 {
				t.Fatalf("rejected CAS writes changed audit: update entries = %d, want 1", updates)
			}
		})

		t.Run("malformed required reference is rejected before null coercion", func(t *testing.T) {
			id := uuid.New()
			err := db.Upsert(ctx, item.Name, id,
				map[string]any{"Name": "bad-ref", "Owner": "not-a-uuid"}, item)
			if !errors.Is(err, storage.ErrRequiredFieldEmpty) {
				t.Fatalf("malformed required reference: got %v, want ErrRequiredFieldEmpty", err)
			}
			if _, err := db.GetByID(ctx, item.Name, id, item); !storage.IsNotFound(err) {
				t.Fatalf("malformed reference was coerced to NULL and persisted: %v", err)
			}

			// Syntactically valid references remain a database-integrity concern;
			// required validation must not mask the existing FK error contract.
			missingID := uuid.New()
			err = db.Upsert(ctx, item.Name, uuid.New(), map[string]any{
				"Name": "missing-target", "Owner": missingID.String(),
			}, item)
			if !errors.Is(err, storage.ErrForeignKeyViolation) {
				t.Fatalf("valid missing reference: got %v, want ErrForeignKeyViolation", err)
			}

			// DSL reference structs are sometimes copied by value although their
			// GetRefUUID method has a pointer receiver. Required validation must
			// accept exactly the representations persistence accepts.
			valueRefID := uuid.New()
			if err := db.Upsert(ctx, item.Name, valueRefID, map[string]any{
				"Name": "value-ref", "Owner": requiredValueRef{id: ownerID.String()}, "Note": "",
			}, item); err != nil {
				t.Fatalf("valid reference value rejected: %v", err)
			}
			got := loadRequiredItem(t, ctx, db, item, valueRefID)
			assertRequiredItem(t, got, "value-ref", ownerID, "")
		})

		t.Run("table part validation precedes replacement delete", func(t *testing.T) {
			id := uuid.New()
			seedRequiredItem(t, ctx, db, item, id, ownerID, "with-lines", "")
			tp := item.TableParts[0]
			original := []map[string]any{{"Product": "original", "Owner": ownerID.String()}}
			if err := db.UpsertTablePartRows(ctx, item.Name, tp.Name, id, original, tp); err != nil {
				t.Fatalf("seed table part: %v", err)
			}

			err := db.UpsertTablePartRows(ctx, item.Name, tp.Name, id,
				[]map[string]any{{"Product": "   ", "Owner": ownerID.String()}}, tp)
			if !errors.Is(err, storage.ErrRequiredFieldEmpty) {
				t.Fatalf("blank required table-part field: got %v, want ErrRequiredFieldEmpty", err)
			}
			rows, err := db.GetTablePartRows(ctx, item.Name, tp.Name, id, tp)
			if err != nil {
				t.Fatalf("reload table part after rejection: %v", err)
			}
			assertRequiredLine(t, rows, "original", ownerID)

			if err := db.UpsertTablePartRows(ctx, item.Name, tp.Name, id,
				[]map[string]any{{"Product": "replacement", "Owner": ownerID.String()}}, tp); err != nil {
				t.Fatalf("valid table-part replacement: %v", err)
			}
			rows, err = db.GetTablePartRows(ctx, item.Name, tp.Name, id, tp)
			if err != nil {
				t.Fatalf("reload valid table part: %v", err)
			}
			assertRequiredLine(t, rows, "replacement", ownerID)
		})

		t.Run("exchange bypasses entity and table part required checks", func(t *testing.T) {
			const source = `["exchange","required-matrix","remote-node",1]`
			id := uuid.New()
			if err := db.ApplyReplicatedEntity(ctx, item.Name, id,
				map[string]any{"Name": "", "Owner": "remote-non-uuid"}, item, source); err != nil {
				t.Fatalf("replicated entity rejected by required backstop: %v", err)
			}
			tp := item.TableParts[0]
			if err := db.ApplyReplicatedTablePartRows(ctx, item.Name, tp.Name, id,
				[]map[string]any{{"Product": "", "Owner": "remote-non-uuid"}}, tp, source); err != nil {
				t.Fatalf("replicated table part rejected by required backstop: %v", err)
			}
		})
	})
}

func seedRequiredItem(t *testing.T, ctx context.Context, db *storage.DB, entity *metadata.Entity,
	id, ownerID uuid.UUID, name, note string) {
	t.Helper()
	if err := db.Upsert(ctx, entity.Name, id, map[string]any{
		"Name":  name,
		"Owner": ownerID.String(),
		"Note":  note,
	}, entity); err != nil {
		t.Fatalf("seed required item: %v", err)
	}
}

func loadRequiredItem(t *testing.T, ctx context.Context, db *storage.DB,
	entity *metadata.Entity, id uuid.UUID) map[string]any {
	t.Helper()
	got, err := db.GetByID(ctx, entity.Name, id, entity)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	return got
}

func assertRequiredItem(t *testing.T, got map[string]any, name string, ownerID uuid.UUID, note string) {
	t.Helper()
	if got["Name"] != name {
		t.Errorf("Name = %#v, want %q", got["Name"], name)
	}
	if got["Owner"] != ownerID.String() {
		t.Errorf("Owner = %#v, want %q", got["Owner"], ownerID.String())
	}
	if got["Note"] != note {
		t.Errorf("Note = %#v, want %q", got["Note"], note)
	}
}

func assertRequiredLine(t *testing.T, rows []map[string]any, product string, ownerID uuid.UUID) {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("table-part row count = %d, want 1: %#v", len(rows), rows)
	}
	if rows[0]["Product"] != product {
		t.Errorf("Product = %#v, want %q", rows[0]["Product"], product)
	}
	if rows[0]["Owner"] != ownerID.String() {
		t.Errorf("Owner = %#v, want %q", rows[0]["Owner"], ownerID.String())
	}
}
