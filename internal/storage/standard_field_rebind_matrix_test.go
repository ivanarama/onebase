package storage_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func numberedRebindEntity(kind metadata.Kind, name string) (*metadata.Entity, string, string) {
	field, fieldID := metadata.StandardCodeField, metadata.StandardCodeFieldID
	if kind == metadata.KindDocument {
		field, fieldID = metadata.StandardNumberField, metadata.StandardNumberFieldID
	}
	return &metadata.Entity{
		Name: name, Kind: kind, Numerator: &metadata.Numerator{Length: 6},
		Fields: []metadata.Field{{ID: fieldID, Name: field, Type: metadata.FieldTypeString}},
	}, field, fieldID
}

func rewriteStandardMapID(t *testing.T, db *storage.DB, table, from, to, fieldType string) {
	t.Helper()
	d := db.Dialect()
	q := fmt.Sprintf(`UPDATE _schema_fields SET field_id=%s, field_type=%s WHERE table_name=%s AND field_id=%s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4))
	tag, err := db.Exec(context.Background(), q, to, fieldType, table, from)
	if err != nil || tag.RowsAffected != 1 {
		t.Fatalf("rewrite schema map: %v, rows=%d", err, tag.RowsAffected)
	}
}

func schemaMapOwners(t *testing.T, db *storage.DB, table, column string) []string {
	t.Helper()
	d := db.Dialect()
	q := fmt.Sprintf(`SELECT field_id FROM _schema_fields WHERE table_name=%s AND column_name=%s ORDER BY field_id`, d.Placeholder(1), d.Placeholder(2))
	rows, err := db.Query(context.Background(), q, table, column)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var owners []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		owners = append(owners, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return owners
}

// A real Migrate created the physical column; an older configurator left its
// schema-map owner as f_*. Dry-run must report just the ID transition and the
// next Migrate must preserve the value on both supported SQL dialects.
func TestStandardFieldRebindMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		for _, tc := range []struct {
			kind metadata.Kind
			name string
		}{
			{metadata.KindCatalog, "RebindCatalog"},
			{metadata.KindDocument, "RebindDocument"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var e *metadata.Entity
				field, standardID := metadata.StandardCodeField, metadata.StandardCodeFieldID
				legacyID := "f_old_" + tc.name
				if tc.kind == metadata.KindCatalog {
					e = writeCatalogProject(t, "name: Ученики\nnumerator: {length: 6}\nfields:\n  - {id: "+legacyID+", name: Код, type: string}\n")
				} else {
					field, standardID = metadata.StandardNumberField, metadata.StandardNumberFieldID
					e = docEntity(t, writeDocProject(t, "name: Реализация\nnumerator: {length: 6}\nfields:\n  - {id: "+legacyID+", name: Номер, type: string}\n"))
				}
				if len(e.Fields) != 1 || e.Fields[0].ID != standardID {
					t.Fatalf("legacy YAML was not normalized by project.Load: %+v", e.Fields)
				}
				if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
					t.Fatal(err)
				}
				rowID := uuid.New()
				if err := db.Upsert(ctx, e.Name, rowID, map[string]any{field: "000123"}, e); err != nil {
					t.Fatal(err)
				}
				table, column := metadata.TableName(e.Name), metadata.ColumnName(e.Fields[0])
				rewriteStandardMapID(t, db, table, standardID, legacyID, "string")

				if _, err := db.PlanTableChanges(ctx, table, e.Fields); err == nil {
					t.Fatal("generic table API rebound a field without entity provenance")
				}
				plan, err := db.PlanMigration(ctx, []*metadata.Entity{e}, nil, nil)
				if err != nil || len(plan) != 1 || plan[0].Kind != storage.ChangeRebindFieldID || plan[0].From != legacyID || plan[0].FieldID != standardID || plan[0].Destructive() {
					t.Fatalf("dry-run = %+v, %v", plan, err)
				}
				if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
					t.Fatal(err)
				}
				if owners := schemaMapOwners(t, db, table, column); len(owners) != 1 || owners[0] != standardID {
					t.Fatalf("owners after rebind = %v", owners)
				}
				row, err := db.GetByID(ctx, e.Name, rowID, e)
				if err != nil || row[field] != "000123" {
					t.Fatalf("value after rebind = %v, %v", row, err)
				}
				plan, err = db.PlanMigration(ctx, []*metadata.Entity{e}, nil, nil)
				if err != nil || len(plan) != 0 {
					t.Fatalf("second dry-run = %+v, %v", plan, err)
				}
			})
		}
	})
}

func TestStandardFieldRebindRejectsAmbiguousMapMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		e, _, standardID := numberedRebindEntity(metadata.KindCatalog, "RebindAmbiguous")
		if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
			t.Fatal(err)
		}
		table, column := metadata.TableName(e.Name), metadata.ColumnName(e.Fields[0])
		rewriteStandardMapID(t, db, table, standardID, "f_first", "string")
		d := db.Dialect()
		q := fmt.Sprintf(`INSERT INTO _schema_fields (table_name, field_id, column_name, field_type) VALUES (%s,%s,%s,%s)`, d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4))
		if _, err := db.Exec(ctx, q, table, "f_second", column, "string"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.PlanMigration(ctx, []*metadata.Entity{e}, nil, nil); err == nil || !strings.Contains(err.Error(), "multiple schema-map owners") {
			t.Fatalf("ambiguous map dry-run error = %v", err)
		}
		if err := db.Migrate(ctx, []*metadata.Entity{e}); err == nil {
			t.Fatal("ambiguous map was mutated")
		}
		if owners := schemaMapOwners(t, db, table, column); len(owners) != 2 || owners[0] != "f_first" || owners[1] != "f_second" {
			t.Fatalf("map changed after rejected migration: %v", owners)
		}
	})
}

func TestStandardFieldRebindRejectsUnsafeStateMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		for _, tc := range []struct {
			name, errorPart string
			setup           func(t *testing.T, db *storage.DB, e *metadata.Entity, table, column, standardID string)
		}{
			{
				name: "Signature", errorPart: "unsafe schema-map rebind",
				setup: func(t *testing.T, db *storage.DB, e *metadata.Entity, table, column, standardID string) {
					rewriteStandardMapID(t, db, table, standardID, "f_old", "number")
				},
			},
			{
				name: "OldIDStillLive", errorPart: "unsafe schema-map rebind",
				setup: func(t *testing.T, db *storage.DB, e *metadata.Entity, table, column, standardID string) {
					rewriteStandardMapID(t, db, table, standardID, "f_old", "string")
					e.Fields = append(e.Fields, metadata.Field{ID: "f_old", Name: "Other", Type: metadata.FieldTypeString})
				},
			},
			{
				name: "MissingColumn", errorPart: "physical column is missing",
				setup: func(t *testing.T, db *storage.DB, e *metadata.Entity, table, column, standardID string) {
					rewriteStandardMapID(t, db, table, standardID, "f_old", "string")
					if _, err := db.Exec(ctx, fmt.Sprintf(`ALTER TABLE "%s" DROP COLUMN "%s"`, table, column)); err != nil {
						t.Fatal(err)
					}
				},
			},
			{
				name: "TargetIDOccupied", errorPart: "числится за убранным полем",
				setup: func(t *testing.T, db *storage.DB, e *metadata.Entity, table, column, standardID string) {
					rewriteStandardMapID(t, db, table, standardID, "f_old", "string")
					d := db.Dialect()
					q := fmt.Sprintf(`INSERT INTO _schema_fields (table_name, field_id, column_name, field_type) VALUES (%s,%s,%s,%s)`, d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4))
					if _, err := db.Exec(ctx, q, table, standardID, column, "string"); err != nil {
						t.Fatal(err)
					}
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				e, _, standardID := numberedRebindEntity(metadata.KindCatalog, "RebindReject"+tc.name)
				if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
					t.Fatal(err)
				}
				table, column := metadata.TableName(e.Name), metadata.ColumnName(e.Fields[0])
				tc.setup(t, db, e, table, column, standardID)
				before := schemaMapOwners(t, db, table, column)
				if _, err := db.PlanMigration(ctx, []*metadata.Entity{e}, nil, nil); err == nil || !strings.Contains(err.Error(), tc.errorPart) {
					t.Fatalf("unsafe dry-run error = %v, want %q", err, tc.errorPart)
				}
				if err := db.Migrate(ctx, []*metadata.Entity{e}); err == nil {
					t.Fatal("unsafe migration succeeded")
				}
				after := schemaMapOwners(t, db, table, column)
				if fmt.Sprint(after) != fmt.Sprint(before) {
					t.Fatalf("schema map changed after rejection: %v → %v", before, after)
				}
			})
		}
	})
}
