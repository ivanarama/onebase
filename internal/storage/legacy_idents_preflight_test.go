package storage_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// SQLite could store both the complete old name and its PostgreSQL prefix.
// Finding the complete name must not hide the other, current owner's data.
func TestMigrateRefusesFullLegacyBesideShortOwner(t *testing.T) {
	for _, kind := range []string{"column", "table"} {
		t.Run(kind, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				if !db.IsSQLite() {
					t.Skip("PostgreSQL cannot store a complete identifier longer than 63 bytes")
				}
				ctx := context.Background()
				short, full := strings.Repeat("Я", 31), strings.Repeat("Я", 31)+"Продолжение"
				owner := &metadata.Entity{Name: "Владелец", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: short, Type: metadata.FieldTypeString}, {Name: full, Type: metadata.FieldTypeString}}}
				entities := []*metadata.Entity{owner}
				shortTable, fullTable := metadata.TableName(owner.Name), metadata.TableName(owner.Name)
				shortCol, fullCol := strings.ToLower(short), strings.ToLower(full)
				if kind == "table" {
					owner.Name = short
					owner.Fields = []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}
					entities = append(entities, &metadata.Entity{Name: full, Kind: metadata.KindCatalog, Fields: owner.Fields})
					shortTable, fullTable = strings.ToLower(short), strings.ToLower(full)
					shortCol, fullCol = "наименование", "наименование"
				}
				if err := metadata.Validate(entities, nil); err != nil {
					t.Fatal(err)
				}
				if err := db.EnsureSchemaMapSchema(ctx); err != nil {
					t.Fatal(err)
				}
				id := uuid.New()
				statements := []string{}
				if kind == "column" {
					statements = append(statements, "CREATE TABLE "+shortTable+" (id TEXT PRIMARY KEY, "+shortCol+" TEXT UNIQUE, "+fullCol+" TEXT UNIQUE)",
						fmt.Sprintf("INSERT INTO %s VALUES ('%s','short data','long data')", shortTable, id))
				} else {
					for i, table := range []string{shortTable, fullTable} {
						statements = append(statements, "CREATE TABLE "+table+" (id TEXT PRIMARY KEY, наименование TEXT UNIQUE)", fmt.Sprintf("INSERT INTO %s VALUES ('%s','%s data')", table, id, []string{"short", "long"}[i]))
					}
				}
				statements = append(statements,
					"CREATE INDEX legacy_short_index ON "+shortTable+" ("+shortCol+")",
					"CREATE INDEX legacy_long_index ON "+fullTable+" ("+fullCol+")",
					"CREATE TABLE legacy_child (value TEXT REFERENCES "+fullTable+" ("+fullCol+"))",
					"INSERT INTO legacy_child VALUES ('long data')")
				for _, sql := range statements {
					if _, err := db.Exec(ctx, sql); err != nil {
						t.Fatal(err)
					}
				}
				// Seed both original physical owners in the durable field map.
				for i, table := range []string{shortTable, fullTable} {
					col := []string{shortCol, fullCol}[i]
					if _, err := db.Exec(ctx, "INSERT INTO _schema_fields (table_name, field_id, column_name, field_type) VALUES (?, ?, ?, ?)", table, fmt.Sprintf("field-%d", i), col, "string"); err != nil {
						t.Fatal(err)
					}
				}
				before := legacySQLiteSnapshot(t, db)
				for attempt := 0; attempt < 2; attempt++ {
					plan, err := db.PlanLongIdents(ctx, entities, nil, nil)
					if err != nil {
						t.Fatal(err)
					}
					conflict := false
					for _, a := range plan {
						if strings.Contains(a.Conflict, "претендуют 2") {
							conflict = true
						}
					}
					if !conflict {
						t.Errorf("attempt %d: missing short-owner conflict: %v", attempt, plan)
					}
					if err := db.CheckLongIdents(ctx, entities, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "КОНФЛИКТ") {
						t.Errorf("attempt %d: preflight must refuse: %v", attempt, err)
					}
					if err := db.Migrate(ctx, entities); err == nil || !strings.Contains(err.Error(), "КОНФЛИКТ") {
						t.Errorf("attempt %d: migration must refuse before ALTER: %v", attempt, err)
					}
					if after := legacySQLiteSnapshot(t, db); !reflect.DeepEqual(before, after) {
						t.Errorf("attempt %d changed schema/indexes/FKs/field map", attempt)
					}
					for i, table := range []string{shortTable, fullTable} {
						var value string
						col := []string{shortCol, fullCol}[i]
						err := db.QueryRow(ctx, "SELECT "+col+" FROM "+table+" WHERE id = ?", id.String()).Scan(&value)
						want := []string{"short data", "long data"}[i]
						if err != nil || value != want {
							t.Errorf("attempt %d: original owner lost: got %q, want %q, err %v", attempt, value, want, err)
						}
					}
					var child string
					if err := db.QueryRow(ctx, "SELECT value FROM legacy_child").Scan(&child); err != nil || child != "long data" {
						t.Errorf("child data changed: %q %v", child, err)
					}
					rows, err := db.Query(ctx, "PRAGMA foreign_key_check")
					if err != nil {
						t.Fatal(err)
					}
					if rows.Next() {
						t.Error("foreign key broken")
					}
					err = rows.Err()
					rows.Close()
					if err != nil {
						t.Fatal(err)
					}
				}
			})
		})
	}
}

func legacySQLiteSnapshot(t *testing.T, db *storage.DB) []string {
	t.Helper()
	var out []string
	for _, q := range []string{
		"SELECT type || ':' || name || ':' || tbl_name || ':' || coalesce(sql,'') FROM sqlite_master ORDER BY type,name",
		"SELECT table_name || ':' || field_id || ':' || column_name || ':' || field_type FROM _schema_fields ORDER BY table_name,field_id",
	} {
		rows, err := db.Query(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			out = append(out, s)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// Checking all candidates must also preserve ambiguity/data guards without a
// short logical owner, including an empty full name hiding populated truncation.
func TestMigrateRefusesMultiplePhysicalLegacyNames(t *testing.T) {
	for _, kind := range []string{"column", "table"} {
		for _, hasTarget := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/target=%v", kind, hasTarget), func(t *testing.T) {
				dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
					if !db.IsSQLite() {
						t.Skip("only SQLite can store both physical legacy candidates")
					}
					ctx := context.Background()
					long := strings.Repeat("Я", 31) + "Продолжение"
					logical := strings.ToLower(long)
					names := metadata.LegacySQLIdents(logical)
					if hasTarget {
						names = append(names, metadata.SQLIdent(logical))
					}
					entity := &metadata.Entity{Name: "Владелец", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: long, Type: metadata.FieldTypeString}}}
					if err := db.EnsureSchemaMapSchema(ctx); err != nil {
						t.Fatal(err)
					}
					var dataQuery string
					if kind == "table" {
						entity.Name = long
						entity.Fields = []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}
						for _, name := range names {
							if _, err := db.Exec(ctx, "CREATE TABLE "+name+" (id TEXT PRIMARY KEY, наименование TEXT)"); err != nil {
								t.Fatal(err)
							}
						}
						if _, err := db.Exec(ctx, "INSERT INTO "+names[1]+" VALUES (?, 'legacy data')", uuid.New().String()); err != nil {
							t.Fatal(err)
						}
						dataQuery = "SELECT наименование FROM " + names[1]
					} else {
						if _, err := db.Exec(ctx, "CREATE TABLE владелец (id TEXT PRIMARY KEY, "+strings.Join(names, " TEXT, ")+" TEXT)"); err != nil {
							t.Fatal(err)
						}
						if _, err := db.Exec(ctx, "INSERT INTO владелец (id, "+names[1]+") VALUES (?, 'legacy data')", uuid.New().String()); err != nil {
							t.Fatal(err)
						}
						dataQuery = "SELECT " + names[1] + " FROM владелец"
					}
					before := legacySQLiteSnapshot(t, db)
					for attempt := 0; attempt < 2; attempt++ {
						if err := db.Migrate(ctx, []*metadata.Entity{entity}); err == nil || !strings.Contains(err.Error(), "КОНФЛИКТ") {
							t.Errorf("ambiguous legacy data must block migration: %v", err)
						}
						if after := legacySQLiteSnapshot(t, db); !reflect.DeepEqual(before, after) {
							t.Error("ambiguous schema changed")
						}
						var value string
						if err := db.QueryRow(ctx, dataQuery).Scan(&value); err != nil || value != "legacy data" {
							t.Errorf("legacy data changed: %q %v", value, err)
						}
					}
				})
			})
		}
	}
}
