package storage_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Removed business fields remain in the schema map without destructive mode.
// A valid UUID in such a field is still business data, never a service revision.
func TestInfoRegEditRevisionLegacyNamesMatrix(t *testing.T) {
	longName := strings.Repeat("ДлинныйРегистр", 6)
	for _, name := range []string{"ShortRevisionLegacy", longName} {
		for _, physical := range []string{"legacy", "truncated", "current"} {
			for _, mapKey := range []string{"logical", "truncated", "current"} {
				t.Run(name+"/"+physical+"/"+mapKey, func(t *testing.T) {
					dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
						ctx := context.Background()
						ir := revisionRegister(name)
						logical, current := metadata.InfoRegTableLogical(name), metadata.InfoRegTableName(name)
						old := logical
						legacy := metadata.LegacySQLIdents(logical)
						if (!db.IsSQLite() || physical == "truncated") && len(legacy) != 0 {
							old = legacy[1]
						}
						table := old
						if physical == "current" {
							table = current
						}
						key := logical
						switch mapKey {
						case "current":
							key = current
						case "truncated":
							if len(legacy) == 0 || (db.IsSQLite() && physical != "truncated") {
								t.Skip("no truncated legacy map owned by this table")
							}
							key = legacy[1]
						}
						seedRevisionLegacyTable(t, db, table, true)
						if err := db.EnsureSchemaMapSchema(ctx); err != nil {
							t.Fatal(err)
						}
						if _, err := db.Exec(ctx, "INSERT INTO _schema_fields (table_name,field_id,column_name,field_type) VALUES ("+db.Dialect().Placeholder(1)+",'removed-business-field','_edit_revision','string')", key); err != nil {
							t.Fatal(err)
						}
						before := currentLegacySchemaSnapshot(t, db)
						data := revisionLegacyRows(t, db, table)
						for attempt := 0; attempt < 2; attempt++ {
							err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir})
							if err == nil || !strings.Contains(err.Error(), "reserved column _edit_revision belongs to a business field") {
								t.Fatalf("business UUID must refuse migration before adoption: %v", err)
							}
							if after := currentLegacySchemaSnapshot(t, db); !reflect.DeepEqual(before, after) {
								t.Fatal("refused migration changed DDL/indexes/field map")
							}
							if after := revisionLegacyRows(t, db, table); !reflect.DeepEqual(data, after) {
								t.Fatal("refused migration changed row count/keys/resources/business UUIDs")
							}
						}
					})
				})
			}
		}
	}

	for _, initial := range []string{"new", "legacy", "truncated-sqlite", "current"} {
		t.Run("positive/"+initial, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				ctx := context.Background()
				ir := revisionRegister(longName)
				logical, current := metadata.InfoRegTableLogical(ir.Name), metadata.InfoRegTableName(ir.Name)
				table := logical
				if !db.IsSQLite() || initial == "truncated-sqlite" {
					table = metadata.LegacySQLIdents(logical)[1]
				}
				if initial == "current" {
					table = current
				}
				if err := db.EnsureSchemaMapSchema(ctx); err != nil {
					t.Fatal(err)
				}
				if initial != "new" {
					seedRevisionLegacyTable(t, db, table, false)
					if _, err := db.Exec(ctx, "INSERT INTO _schema_fields (table_name,field_id,column_name,field_type) VALUES ("+db.Dialect().Placeholder(1)+",'key','key','string'),("+db.Dialect().Placeholder(2)+",'value','value','string')", table, table); err != nil {
						t.Fatal(err)
					}
				}
				// Both cancellation and a caller rollback must leave the complete
				// legacy DDL and map untouched, including after successful rename.
				before := currentLegacySchemaSnapshot(t, db)
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				if err := db.MigrateInfoRegisters(canceled, []*metadata.InfoRegister{ir}); err == nil {
					t.Fatal("canceled migration succeeded")
				}
				sentinel := errors.New("rollback legacy migration")
				if err := db.WithTxScope(ctx, func(txCtx context.Context) error {
					if err := db.MigrateInfoRegisters(txCtx, []*metadata.InfoRegister{ir}); err != nil {
						return err
					}
					return sentinel
				}); !errors.Is(err, sentinel) {
					t.Fatal(err)
				}
				if after := currentLegacySchemaSnapshot(t, db); !reflect.DeepEqual(before, after) {
					t.Fatal("cancellation/rollback changed schema or map")
				}
				if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
					t.Fatal(err)
				}
				if initial != "new" {
					first := readRevision(t, db, ir, storage.InfoRegEditKey{Dimensions: map[string]any{"Key": "first"}})
					second := readRevision(t, db, ir, storage.InfoRegEditKey{Dimensions: map[string]any{"Key": "second"}})
					if first.Revision == second.Revision {
						t.Fatal("backfill reused UUID for different rows")
					}
				}
				for _, k := range []string{"first", "second"} {
					key := storage.InfoRegEditKey{Dimensions: map[string]any{"Key": k}}
					if initial == "new" {
						if err := db.InfoRegSet(ctx, ir, key.Dimensions, map[string]any{"Value": "keep-" + k}, nil); err != nil {
							t.Fatal(err)
						}
					}
					first := readRevision(t, db, ir, key)
					if first.Row["Value"] != "keep-"+k {
						t.Fatalf("lost legacy resource: %v", first.Row)
					}
					if err := db.InfoRegSet(ctx, ir, key.Dimensions, map[string]any{"Value": "updated-" + k}, nil); err != nil {
						t.Fatal(err)
					}
					second := readRevision(t, db, ir, key)
					if second.Revision == first.Revision || second.Row["Value"] != "updated-"+k {
						t.Fatal("ordinary write did not rotate revision and preserve resource")
					}
					if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
						t.Fatal(err)
					}
					if readRevision(t, db, ir, key).Revision != second.Revision {
						t.Fatal("repeat migration changed revision")
					}
				}
				rows := revisionLegacyRows(t, db, current)
				if len(rows) != 2 || rows[0][2] == rows[1][2] {
					t.Fatalf("backfill/writer reused revision or lost rows: %v", rows)
				}
				if initial != "new" && table != current {
					if exists, err := db.TableExists(ctx, table); err != nil || exists {
						t.Fatalf("legacy table remained: %v %v", exists, err)
					}
				}
			})
		})
	}

	t.Run("ambiguous-owners", func(t *testing.T) {
		dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
			ctx := context.Background()
			ir := revisionRegister(longName)
			legacy := metadata.LegacySQLIdents(metadata.InfoRegTableLogical(ir.Name))[1]
			short := revisionRegister(strings.TrimPrefix(legacy, "инфо_"))
			if metadata.InfoRegTableName(short.Name) != legacy {
				t.Fatal("fixture does not share physical name")
			}
			seedRevisionLegacyTable(t, db, legacy, true)
			if err := db.EnsureSchemaMapSchema(ctx); err != nil {
				t.Fatal(err)
			}
			before, data := currentLegacySchemaSnapshot(t, db), revisionLegacyRows(t, db, legacy)
			if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir, short}); err == nil || !strings.Contains(err.Error(), "КОНФЛИКТ") {
				t.Fatalf("ambiguous owner accepted: %v", err)
			}
			if !reflect.DeepEqual(before, currentLegacySchemaSnapshot(t, db)) || !reflect.DeepEqual(data, revisionLegacyRows(t, db, legacy)) {
				t.Fatal("ambiguous owner changed schema/map/data")
			}
		})
	})
}

func seedRevisionLegacyTable(t *testing.T, db *storage.DB, table string, collision bool) {
	t.Helper()
	ctx := context.Background()
	extra, values := "", ""
	if collision {
		extra = ", _edit_revision TEXT"
		values = ", '11111111-1111-4111-8111-111111111111'"
	}
	for _, q := range []string{
		"CREATE TABLE " + table + " (key TEXT PRIMARY KEY, value TEXT" + extra + ")",
		"CREATE INDEX revision_legacy_value ON " + table + " (value)",
		"INSERT INTO " + table + " VALUES ('first','keep-first'" + values + "),('second','keep-second'" + values + ")",
	} {
		if _, err := db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
}

func revisionLegacyRows(t *testing.T, db *storage.DB, table string) [][3]string {
	t.Helper()
	rows, err := db.Query(context.Background(), fmt.Sprintf("SELECT key,value,_edit_revision FROM %s ORDER BY key", table))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][3]string
	for rows.Next() {
		var row [3]string
		if err := rows.Scan(&row[0], &row[1], &row[2]); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
