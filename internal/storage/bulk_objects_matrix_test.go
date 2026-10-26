package storage_test

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestGetByIDsFilteredMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, kind := range []metadata.Kind{metadata.KindCatalog, metadata.KindDocument} {
			entity := &metadata.Entity{Name: "BulkObjects" + uuid.NewString()[:8], Kind: kind,
				Hierarchical: kind == metadata.KindCatalog,
				Fields: []metadata.Field{
					{Name: "Label", Type: metadata.FieldTypeString},
					{Name: "Scope", Type: metadata.FieldTypeString},
					{Name: "Amount", Type: metadata.FieldTypeNumber},
					{Name: "Enabled", Type: metadata.FieldTypeBool},
					{Name: "Day", Type: metadata.FieldTypeDate},
				}}
			if err := db.Migrate(t.Context(), []*metadata.Entity{entity}); err != nil {
				t.Fatal(err)
			}
			allowed, denied, missing := uuid.New(), uuid.New(), uuid.New()
			for i, id := range []uuid.UUID{allowed, denied} {
				values := map[string]any{"Label": "object", "Scope": "allowed", "Amount": "12.50", "Enabled": true, "Day": "2026-10-09"}
				if i == 1 {
					values["Scope"] = "denied"
				}
				if entity.Hierarchical {
					values["is_folder"] = i == 0
					if i == 1 {
						values["parent_id"] = allowed.String()
					}
				}
				if err := db.Upsert(t.Context(), entity.Name, id, values, entity); err != nil {
					t.Fatal(err)
				}
			}
			if kind == metadata.KindDocument {
				if err := db.SetPosted(t.Context(), entity.Name, allowed, true); err != nil {
					t.Fatal(err)
				}
			}
			ids := []uuid.UUID{denied, allowed, missing, allowed}
			rows, err := db.GetByIDsFiltered(t.Context(), entity, ids, nil)
			if err != nil || len(rows) != 2 {
				t.Fatalf("batch rows=%v err=%v", rows, err)
			}
			if rows[allowed.String()]["_version"] != int64(1) {
				t.Fatalf("missing or mistyped object version: %#v", rows[allowed.String()])
			}
			if kind == metadata.KindDocument && rows[allowed.String()]["posted"] != true {
				t.Fatalf("posted flag lost: %#v", rows[allowed.String()])
			}
			for _, id := range []uuid.UUID{allowed, denied} {
				want, err := db.GetByID(t.Context(), entity.Name, id, entity)
				if err != nil || !reflect.DeepEqual(rows[id.String()], want) {
					t.Fatalf("%s full object=%#v want=%#v err=%v", kind, rows[id.String()], want, err)
				}
			}
			rows, err = db.GetByIDsFiltered(t.Context(), entity, ids, &storage.Predicate{Field: "Scope", Op: "eq", Value: "allowed"})
			if err != nil || len(rows) != 1 || rows[allowed.String()] == nil {
				t.Fatalf("filtered rows=%v err=%v", rows, err)
			}
			if _, err := db.GetByIDsFiltered(t.Context(), entity, ids, &storage.Predicate{Field: "Missing", Op: "eq", Value: "x"}); err == nil {
				t.Fatal("invalid predicate must fail closed")
			}
			if rows, err := db.GetByIDsFiltered(t.Context(), entity, nil, nil); err != nil || len(rows) != 0 {
				t.Fatalf("empty batch=%v err=%v", rows, err)
			}
		}
	})
}

func TestGetByIDsFilteredReferencePolicyMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		entity := &metadata.Entity{Name: "BulkPolicy" + uuid.NewString()[:8], Kind: metadata.KindCatalog}
		label := metadata.Field{Name: "Label", Type: metadata.FieldTypeString}
		entity.Fields = []metadata.Field{label,
			{Name: "Audience", Type: metadata.FieldTypeString},
			{Name: "Owner", Type: metadata.FieldType("reference:" + entity.Name), RefEntity: entity.Name},
		}
		if err := db.Migrate(t.Context(), []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		allowedOwner, deniedOwner, allowed, denied := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		for _, row := range []struct {
			id, owner uuid.UUID
			audience  string
		}{
			{allowedOwner, allowedOwner, "anna"}, {deniedOwner, deniedOwner, "other"},
			{allowed, allowedOwner, "other"}, {denied, deniedOwner, "anna"},
		} {
			if err := db.Upsert(t.Context(), entity.Name, row.id, map[string]any{
				"Label": row.id.String(), "Audience": row.audience, "Owner": row.owner.String(),
			}, entity); err != nil {
				t.Fatal(err)
			}
		}
		predicate := &storage.Predicate{Field: "Owner", RefEntity: entity,
			RefPredicate: &storage.Predicate{Field: "Audience", Op: "eq", Value: "anna"}}
		ids := []uuid.UUID{denied, allowed}
		for _, full := range []bool{false, true} {
			var rows map[string]map[string]any
			var err error
			if full {
				rows, err = db.GetByIDsFiltered(t.Context(), entity, ids, predicate)
			} else {
				rows, err = db.GetFieldsByIDsFiltered(t.Context(), entity, ids, []metadata.Field{label}, predicate)
			}
			if err != nil || len(rows) != 1 || rows[allowed.String()] == nil {
				t.Fatalf("full=%t: reference policy must use the outer object's owner: rows=%v err=%v", full, rows, err)
			}
		}
	})
}
