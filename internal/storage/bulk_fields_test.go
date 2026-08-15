package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestGetFieldsByIDs(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "bulk.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	entity := &metadata.Entity{
		Name: "Номенклатура",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Артикул", Type: metadata.FieldTypeString},
			{Name: "Цена", Type: metadata.FieldTypeNumber},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatal(err)
	}
	id1 := uuid.New()
	id2 := uuid.New()
	if err := db.Upsert(ctx, entity.Name, id1, map[string]any{"Наименование": "Стул", "Артикул": "A1", "Цена": 10}, entity); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, entity.Name, id2, map[string]any{"Наименование": "Стол", "Артикул": "B2", "Цена": 20}, entity); err != nil {
		t.Fatal(err)
	}

	rows, err := db.GetFieldsByIDs(ctx, entity, []uuid.UUID{id1, id2}, []metadata.Field{entity.Fields[0], entity.Fields[2]})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows len = %d, want 2", len(rows))
	}
	row := rows[id1.String()]
	if row["Наименование"] != "Стул" {
		t.Fatalf("Наименование = %v, want Стул", row["Наименование"])
	}
	if _, exists := row["Артикул"]; exists {
		t.Fatalf("Артикул must not be selected: %+v", row)
	}
	if row["id"] != id1.String() {
		t.Fatalf("id = %v, want %s", row["id"], id1.String())
	}
}

func TestGetFieldsByIDsFilteredAppliesPredicateInBatch(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "bulk-filtered.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	entity := &metadata.Entity{
		Name: "Номенклатура",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Область", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatal(err)
	}
	visibleID, hiddenID := uuid.New(), uuid.New()
	if err := db.Upsert(ctx, entity.Name, visibleID, map[string]any{"Наименование": "Visible", "Область": "allowed"}, entity); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, entity.Name, hiddenID, map[string]any{"Наименование": "Hidden", "Область": "denied"}, entity); err != nil {
		t.Fatal(err)
	}

	rows, err := db.GetFieldsByIDsFiltered(ctx, entity, []uuid.UUID{visibleID, hiddenID}, entity.Fields[:1], &Predicate{
		Field: "Область", Op: "eq", Value: "allowed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[visibleID.String()]["Наименование"] != "Visible" {
		t.Fatalf("filtered rows = %#v, want only visible row", rows)
	}
	if _, ok := rows[hiddenID.String()]; ok {
		t.Fatalf("row filter returned hidden id: %#v", rows)
	}

	if _, err := db.GetFieldsByIDsFiltered(ctx, entity, []uuid.UUID{visibleID}, entity.Fields[:1], &Predicate{
		Field: "НетТакогоПоля", Op: "eq", Value: "x",
	}); err == nil {
		t.Fatal("unknown predicate field must fail closed")
	}
}
