package storage_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func managedCatalog() *metadata.Entity {
	e := indexedCatalog("Managed"+strings.ReplaceAll(uuid.NewString()[:8], "-", ""), nil)
	for k := range e.Fields {
		e.Fields[k].ID = fmt.Sprintf("field%d", k)
	}
	return e
}
func managedMigrate(t *testing.T, db *storage.DB, es ...*metadata.Entity) {
	t.Helper()
	require.NoError(t, db.Migrate(t.Context(), es))
}
func managedExec(t *testing.T, db *storage.DB, q string, args ...any) {
	t.Helper()
	_, err := db.Exec(t.Context(), q, args...)
	require.NoError(t, err)
}
func managedRegistry(t *testing.T, db *storage.DB) string {
	t.Helper()
	rows, err := db.Query(t.Context(), "SELECT table_name,index_name,definition,signature,source FROM _managed_entity_indexes ORDER BY table_name,index_name")
	require.NoError(t, err)
	defer rows.Close()
	var out strings.Builder
	for rows.Next() {
		var table, name, def, sig, source string
		require.NoError(t, rows.Scan(&table, &name, &def, &sig, &source))
		fmt.Fprintf(&out, "%s/%s/%s/%s/%s\n", table, name, def, sig, source)
	}
	require.NoError(t, rows.Err())
	return out.String()
}
func managedActual(t *testing.T, db *storage.DB, es ...*metadata.Entity) string {
	t.Helper()
	actions, _, err := db.PlanEntityIndexes(t.Context(), es)
	require.NoError(t, err)
	b, err := json.Marshal(actions)
	require.NoError(t, err)
	return string(b)
}
func managedManifest(t *testing.T, db *storage.DB, e *metadata.Entity) storage.IndexManifest {
	t.Helper()
	_, m, err := db.PlanEntityIndexes(t.Context(), []*metadata.Entity{e})
	require.NoError(t, err)
	return m
}
func managedPut(t *testing.T, db *storage.DB, e *metadata.Entity, values map[string]any) error {
	t.Helper()
	return db.Upsert(t.Context(), e.Name, uuid.New(), values, e)
}

func TestManagedIndexes_ReconcileMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		e := managedCatalog()
		e.Indexes = []metadata.IndexSpec{{Fields: []string{"ИНН"}, Unique: true}}
		managedMigrate(t, db, e)
		managedExec(t, db, "CREATE INDEX external_tax ON "+metadata.TableName(e.Name)+" (инн)")
		registry := managedRegistry(t, db)
		actual := managedActual(t, db, e)
		managedMigrate(t, db, e)
		require.Equal(t, registry, managedRegistry(t, db))
		require.Equal(t, actual, managedActual(t, db, e))
		require.NoError(t, managedPut(t, db, e, map[string]any{"ИНН": "same", "Слаг": "a"}))
		require.Error(t, managedPut(t, db, e, map[string]any{"ИНН": "same", "Слаг": "b"}))
		e.Indexes[0].Unique = false
		managedMigrate(t, db, e)
		require.NoError(t, managedPut(t, db, e, map[string]any{"ИНН": "same", "Слаг": "b"}))
		e.Indexes = []metadata.IndexSpec{{Fields: []string{"ИНН", "Слаг"}, Unique: true}}
		managedMigrate(t, db, e)
		e.Indexes[0].Fields = []string{"Слаг", "ИНН"}
		managedMigrate(t, db, e)
		actions, _, err := db.PlanEntityIndexes(t.Context(), []*metadata.Entity{e})
		require.NoError(t, err)
		managed := 0
		external := 0
		for _, a := range actions {
			if a.Ownership == "managed" {
				managed++
				require.Equal(t, []string{"слаг", "инн"}, a.Index.Columns)
			}
			if a.Index.Name == "external_tax" {
				external++
			}
		}
		require.Equal(t, 1, managed)
		require.Equal(t, 1, external)
		e.Indexes = nil
		managedMigrate(t, db, e)
		require.Empty(t, managedRegistry(t, db))
		require.Contains(t, managedActual(t, db, e), "external_tax")
	})
}

func TestManagedIndexes_AtomicBatchFailureMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		a, b := managedCatalog(), managedCatalog()
		a.Indexes = []metadata.IndexSpec{{Fields: []string{"ИНН"}, Unique: true}}
		managedMigrate(t, db, a, b)
		require.NoError(t, managedPut(t, db, a, map[string]any{"ИНН": "kept"}))
		for k := 0; k < 2; k++ {
			require.NoError(t, managedPut(t, db, b, map[string]any{"ИНН": "duplicate"}))
		}
		before := managedRegistry(t, db)
		old := *a
		a.Indexes = nil
		b.Indexes = []metadata.IndexSpec{{Fields: []string{"ИНН"}, Unique: true}}
		require.Error(t, db.Migrate(t.Context(), []*metadata.Entity{a, b}))
		require.Equal(t, before, managedRegistry(t, db))
		require.Error(t, managedPut(t, db, &old, map[string]any{"ИНН": "kept"}))
	})
}

func TestManagedIndexes_CollisionAndOwnershipMismatchMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		e := managedCatalog()
		managedMigrate(t, db, e)
		e.Indexes = []metadata.IndexSpec{{Fields: []string{"ИНН"}, Unique: true}}
		actions, _, err := db.PlanEntityIndexes(t.Context(), []*metadata.Entity{e})
		require.NoError(t, err)
		var desired storage.EntityIndexDefinition
		for _, a := range actions {
			if a.Action == "create" {
				desired = a.Index
			}
		}
		require.NotEmpty(t, desired.Name)
		managedExec(t, db, "CREATE UNIQUE INDEX "+desired.Name+" ON "+metadata.TableName(e.Name)+" (слаг)")
		require.ErrorContains(t, db.Migrate(t.Context(), []*metadata.Entity{e}), "collision")
		require.Empty(t, managedRegistry(t, db))
		managedExec(t, db, "DROP INDEX "+desired.Name)
		managedMigrate(t, db, e)
		before := managedRegistry(t, db)
		managedExec(t, db, "DROP INDEX "+desired.Name)
		managedExec(t, db, "CREATE UNIQUE INDEX "+desired.Name+" ON "+metadata.TableName(e.Name)+" (слаг)")
		require.ErrorContains(t, db.Migrate(t.Context(), []*metadata.Entity{e}), "differs from catalog")
		require.Equal(t, before, managedRegistry(t, db))
	})
}

func TestManagedIndexes_LegacyAdoptionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		e := managedCatalog()
		managedMigrate(t, db, e)
		// Exactly the old CREATE-only DDL; it deliberately has no registry row.
		managedExec(t, db, storage.CreateEntityIndexSQL(metadata.TableName(e.Name), []string{"инн"}, true))
		managedExec(t, db, "CREATE UNIQUE INDEX external_slug ON "+metadata.TableName(e.Name)+" (слаг)")
		require.NoError(t, managedPut(t, db, e, map[string]any{"ИНН": "legacy", "Слаг": "one"}))
		require.ErrorContains(t, db.Migrate(t.Context(), []*metadata.Entity{e}), "legacy UNIQUE retained")
		require.Error(t, managedPut(t, db, e, map[string]any{"ИНН": "legacy", "Слаг": "two"}))
		m := managedManifest(t, db, e)
		require.Len(t, m.Indexes, 1)
		bad := m.Indexes[0]
		bad.SHA256 = strings.Repeat("0", 64)
		db.SetSchemaOptions(storage.SchemaOptions{AdoptIndexes: []storage.IndexAdoption{bad}})
		require.Error(t, db.Migrate(t.Context(), []*metadata.Entity{e}))
		require.Empty(t, managedRegistry(t, db))
		db.SetSchemaOptions(storage.SchemaOptions{AdoptIndexes: m.Indexes})
		managedMigrate(t, db, e)
		require.NoError(t, managedPut(t, db, e, map[string]any{"ИНН": "legacy", "Слаг": "two"}))
		require.Error(t, managedPut(t, db, e, map[string]any{"ИНН": "different", "Слаг": "one"}))
		require.Empty(t, managedRegistry(t, db))
		require.Contains(t, managedActual(t, db, e), "external_slug")
	})
}

func TestManagedIndexes_StaleManifestMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		e := managedCatalog()
		managedMigrate(t, db, e)
		sql := storage.CreateEntityIndexSQL(metadata.TableName(e.Name), []string{"инн"}, true)
		managedExec(t, db, sql)
		m := managedManifest(t, db, e)
		require.Len(t, m.Indexes, 1)
		name := m.Indexes[0].Index.Name
		managedExec(t, db, "DROP INDEX "+name)
		managedExec(t, db, "CREATE UNIQUE INDEX "+name+" ON "+metadata.TableName(e.Name)+" (слаг)")
		db.SetSchemaOptions(storage.SchemaOptions{AdoptIndexes: m.Indexes})
		require.Error(t, db.Migrate(t.Context(), []*metadata.Entity{e}))
		require.Empty(t, managedRegistry(t, db))
		require.Contains(t, managedActual(t, db, e), "слаг")
	})
}

func TestManagedIndexes_ColumnPreflightMatrix(t *testing.T) {
	for _, ownership := range []string{"managed", "legacy", "external", "expression", "partial", "constraint"} {
		for _, change := range []string{"rename", "retype", "drop"} {
			t.Run(ownership+"/"+change, func(t *testing.T) {
				dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
					e := managedCatalog()
					old := *e
					old.Fields = append([]metadata.Field{}, e.Fields...)
					if ownership == "managed" {
						e.Indexes = []metadata.IndexSpec{{Fields: []string{"ИНН"}, Unique: true}}
					}
					managedMigrate(t, db, e)
					table := metadata.TableName(e.Name)
					switch ownership {
					case "legacy":
						managedExec(t, db, storage.CreateEntityIndexSQL(table, []string{"инн"}, true))
					case "external":
						managedExec(t, db, "CREATE UNIQUE INDEX external_guard ON "+table+" (инн)")
					case "expression":
						managedExec(t, db, "CREATE INDEX external_guard ON "+table+" (lower(инн))")
					case "partial":
						managedExec(t, db, "CREATE INDEX external_guard ON "+table+" (слаг) WHERE инн IS NOT NULL")
					case "constraint":
						if db.IsSQLite() {
							managedExec(t, db, "DROP TABLE "+table)
							ddl := storage.CreateTableSQL(db.Dialect(), e)
							pos := strings.LastIndex(ddl, ")")
							managedExec(t, db, ddl[:pos]+", UNIQUE (инн)"+ddl[pos:])
							managedMigrate(t, db, e)
						} else {
							managedExec(t, db, "ALTER TABLE "+table+" ADD CONSTRAINT external_guard UNIQUE (инн)")
						}
					}
					require.NoError(t, managedPut(t, db, e, map[string]any{"ИНН": "true"}))
					original := managedActual(t, db, e)
					registry := managedRegistry(t, db)
					fieldsBefore, _ := db.PlanMigration(t.Context(), []*metadata.Entity{e}, nil, nil)
					sentinel := managedCatalog()
					e.Fields = append([]metadata.Field{}, e.Fields...)
					e.Indexes = nil
					switch change {
					case "rename":
						e.Fields[1].Name = "NewTax"
					case "retype":
						e.Fields[1].Type = metadata.FieldTypeBool
					case "drop":
						e.Fields = append(e.Fields[:1], e.Fields[2:]...)
					}
					db.SetSchemaOptions(storage.SchemaOptions{AllowDestructive: true})
					err := db.Migrate(t.Context(), []*metadata.Entity{sentinel, e})
					require.ErrorContains(t, err, "blocked by index")
					exists, err := db.TableExists(t.Context(), metadata.TableName(sentinel.Name))
					require.NoError(t, err)
					require.False(t, exists, "preflight must precede all DDL")
					require.Equal(t, registry, managedRegistry(t, db))
					// Use original metadata to verify physical definitions and field map.
					old.Indexes = nil
					if ownership == "managed" {
						old.Indexes = []metadata.IndexSpec{{Fields: []string{"ИНН"}, Unique: true}}
					}
					require.Equal(t, original, managedActual(t, db, &old))
					fieldsAfter, err := db.PlanMigration(t.Context(), []*metadata.Entity{&old}, nil, nil)
					require.NoError(t, err)
					require.Equal(t, fieldsBefore, fieldsAfter)
					var tax string
					require.NoError(t, db.QueryRow(t.Context(), "SELECT инн FROM "+table).Scan(&tax))
					require.Equal(t, "true", tax)
				})
			})
		}
	}
}

func TestManagedIndexes_SeparateRemovalThenRetypeMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		e := managedCatalog()
		e.Indexes = []metadata.IndexSpec{{Fields: []string{"ИНН"}, Unique: true}}
		managedMigrate(t, db, e)
		require.NoError(t, managedPut(t, db, e, map[string]any{"ИНН": "true"}))
		e.Indexes = nil
		managedMigrate(t, db, e)
		e.Fields[1].Type = metadata.FieldTypeBool
		managedMigrate(t, db, e)
		e.Indexes = []metadata.IndexSpec{{Fields: []string{"ИНН"}, Unique: true}}
		managedMigrate(t, db, e)
		require.Error(t, managedPut(t, db, e, map[string]any{"ИНН": true}))
	})
}

func TestManagedIndexes_NumeratorOverlapAndLegacyScopeMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		e := uniqueCatalog("Number"+uuid.NewString()[:8], true)
		e.Indexes = []metadata.IndexSpec{{Fields: []string{metadata.StandardCodeField}, Unique: true}}
		managedMigrate(t, db, e)
		before := managedRegistry(t, db)
		e.Numerator.Unique = false
		managedMigrate(t, db, e)
		require.Equal(t, before, managedRegistry(t, db))
		e.Indexes = nil
		managedMigrate(t, db, e)
		require.Empty(t, managedRegistry(t, db))
		// Legacy unique code must never be removed by the former guessed-name path.
		managedExec(t, db, storage.CreateEntityIndexSQL(metadata.TableName(e.Name), []string{"код"}, true))
		require.ErrorContains(t, db.Migrate(t.Context(), []*metadata.Entity{e}), "legacy UNIQUE retained")
		e.Numerator.Unique = true
		e.Numerator.Scope = "Наименование"
		require.ErrorContains(t, db.Migrate(t.Context(), []*metadata.Entity{e}), "legacy UNIQUE retained")
	})
}

func TestManagedIndexes_RebuildKeepsUnrelatedMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		parent, child := managedCatalog(), managedCatalog()
		child.Fields = append(child.Fields, metadata.Field{ID: "parent", Name: "Owner", Type: metadata.FieldType("reference"), RefEntity: parent.Name})
		managedMigrate(t, db, parent, child)
		managedExec(t, db, "CREATE UNIQUE INDEX external_unrelated ON "+metadata.TableName(child.Name)+" (инн)")
		old := managedActual(t, db, child)
		require.NoError(t, managedPut(t, db, child, map[string]any{"ИНН": "preserved"}))
		child.Fields = child.Fields[:len(child.Fields)-1]
		db.SetSchemaOptions(storage.SchemaOptions{AllowDestructive: true})
		managedMigrate(t, db, parent, child)
		require.Equal(t, old, managedActual(t, db, child))
		require.Error(t, managedPut(t, db, child, map[string]any{"ИНН": "preserved"}))
	})
}

// Borrowed transactions must not deadlock a one-connection SQLite pool.
func TestManagedIndexes_BorrowedTransactionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		e := managedCatalog()
		e.Indexes = []metadata.IndexSpec{{Fields: []string{"ИНН"}, Unique: true}}
		require.NoError(t, db.WithTx(context.Background(), func(ctx context.Context) error { return db.Migrate(ctx, []*metadata.Entity{e}) }))
	})
}

func TestManagedIndexes_NotReadyPreservesExistingNumeratorMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		e := uniqueCatalog("NotReady"+uuid.NewString()[:8], true)
		managedMigrate(t, db, e)
		// NULL does not violate SQL UNIQUE, but the numerator's readiness check
		// must fail without discarding the already active uniqueness guarantee.
		require.NoError(t, managedPut(t, db, e, map[string]any{"Наименование": "empty"}))
		before := managedRegistry(t, db)
		actual := managedActual(t, db, e)
		require.ErrorIs(t, db.Migrate(t.Context(), []*metadata.Entity{e}), storage.ErrCodeUniqueNotReady)
		require.Equal(t, before, managedRegistry(t, db))
		require.Equal(t, actual, managedActual(t, db, e))
	})
}

func TestManagedIndexes_AdoptionRejectsNonPlainMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		e := managedCatalog()
		managedMigrate(t, db, e)
		// Even a convincing generated name cannot authorize a partial index.
		name := metadata.StableEntityIndexName(metadata.TableName(e.Name), []string{"инн"}, true)
		managedExec(t, db, "CREATE UNIQUE INDEX "+name+" ON "+metadata.TableName(e.Name)+" (инн) WHERE инн IS NOT NULL")
		actions, m, err := db.PlanEntityIndexes(t.Context(), []*metadata.Entity{e})
		require.NoError(t, err)
		require.Empty(t, m.Indexes)
		var physical storage.EntityIndexDefinition
		for _, a := range actions {
			if a.Index.Name == name {
				physical = a.Index
			}
		}
		require.NotEmpty(t, physical.Name)
		db.SetSchemaOptions(storage.SchemaOptions{AdoptIndexes: []storage.IndexAdoption{{Index: physical, SHA256: physical.Fingerprint()}}})
		require.Error(t, db.Migrate(t.Context(), []*metadata.Entity{e}))
		require.Empty(t, managedRegistry(t, db))
	})
}

func TestManagedIndexes_NotReadyRollsBackOtherEntityMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		a := managedCatalog()
		a.Indexes = []metadata.IndexSpec{{Fields: []string{"ИНН"}, Unique: true}}
		b := uniqueCatalog("LateNumber"+uuid.NewString()[:8], true)
		managedMigrate(t, db, a, b)
		require.NoError(t, managedPut(t, db, b, map[string]any{"Наименование": "no code"}))
		before := managedRegistry(t, db)
		a.Indexes = nil
		require.ErrorIs(t, db.Migrate(t.Context(), []*metadata.Entity{a, b}), storage.ErrCodeUniqueNotReady)
		require.Equal(t, before, managedRegistry(t, db))
	})
}

func TestManagedIndexes_RecheckAfterExternalDDLMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		parent, child := managedCatalog(), managedCatalog()
		child.Fields = append(child.Fields, metadata.Field{ID: "parent", Name: "Owner", Type: metadata.FieldType("reference"), RefEntity: parent.Name})
		managedMigrate(t, db, parent, child)
		require.NoError(t, managedPut(t, db, child, map[string]any{"ИНН": "true"}))
		var externalExec func(string) error
		if db.IsSQLite() {
			conn, err := sql.Open("sqlite", db.SQLitePath())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, conn.Close()) })
			externalExec = func(q string) error { _, err := conn.ExecContext(t.Context(), q); return err }
		} else {
			var schema string
			require.NoError(t, db.QueryRow(t.Context(), "SELECT current_schema()").Scan(&schema))
			conn, err := pgx.Connect(t.Context(), os.Getenv("TEST_DATABASE_URL"))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, conn.Close(context.Background())) })
			_, err = conn.Exec(t.Context(), "SET search_path TO "+pgx.Identifier{schema}.Sanitize())
			require.NoError(t, err)
			externalExec = func(q string) error { _, err := conn.Exec(t.Context(), q); return err }
		}
		old := *child
		old.Fields = append([]metadata.Field{}, child.Fields...)
		parent.Fields = append(parent.Fields, metadata.Field{ID: "new", Name: "Extra", Type: metadata.FieldTypeString})
		child.Fields[1].Type = metadata.FieldTypeBool
		injected := false
		db.SetSchemaOptions(storage.SchemaOptions{Report: func(c storage.SchemaChange, applied bool) {
			if c.Table == metadata.TableName(parent.Name) && applied && !injected {
				// The read-only preflight has already passed. This noncooperating
				// writer commits a new dependency before the child table's DDL lock.
				require.NoError(t, externalExec("CREATE UNIQUE INDEX late_guard ON "+metadata.TableName(child.Name)+" (инн)"))
				injected = true
			}
		}})
		require.ErrorContains(t, db.Migrate(t.Context(), []*metadata.Entity{parent, child}), "blocked by index late_guard")
		require.True(t, injected)
		changes, err := db.PlanMigration(t.Context(), []*metadata.Entity{&old}, nil, nil)
		require.NoError(t, err)
		require.Empty(t, changes)
		var value string
		require.NoError(t, db.QueryRow(t.Context(), "SELECT инн FROM "+metadata.TableName(child.Name)).Scan(&value))
		require.Equal(t, "true", value)
	})
}

// Rebinding a legacy standard-field ID only updates the schema map; even an
// opaque external index must survive because no physical column changes.
func TestManagedIndexes_StandardFieldRebindMatrix(t *testing.T) {
	for _, kind := range []metadata.Kind{metadata.KindCatalog, metadata.KindDocument} {
		for _, index := range []struct {
			name string
			sql  string
		}{
			{"plain", "(name)"},
			{"partial", "(name) WHERE name IS NOT NULL"},
			{"expression", "(lower(name))"},
		} {
			t.Run(string(kind)+"/"+index.name, func(t *testing.T) {
				dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
					e, field, standardID := numberedRebindEntity(kind, "RebindIndexed")
					e.Fields = append(e.Fields, metadata.Field{ID: "field_name", Name: "Name", Type: metadata.FieldTypeString})
					managedMigrate(t, db, e)
					table := metadata.TableName(e.Name)
					managedExec(t, db, "CREATE INDEX external_rebind ON "+table+" "+index.sql)
					rowID := uuid.New()
					require.NoError(t, db.Upsert(t.Context(), e.Name, rowID, map[string]any{field: "000123", "Name": "kept"}, e))
					indexesBefore := managedActual(t, db, e)
					registryBefore := managedRegistry(t, db)
					rewriteStandardMapID(t, db, table, standardID, "f_legacy", "string")
					plan, err := db.PlanMigration(t.Context(), []*metadata.Entity{e}, nil, nil)
					require.NoError(t, err)
					require.Len(t, plan, 1)
					require.Equal(t, storage.ChangeRebindFieldID, plan[0].Kind)
					require.Equal(t, "f_legacy", plan[0].From)
					require.Equal(t, standardID, plan[0].FieldID)

					previewErr := db.CheckEntityColumnMigration(t.Context(), e)
					managedMigrate(t, db, e)
					require.NoError(t, previewErr, "preview must allow metadata-only rebind")
					require.Equal(t, []string{standardID}, schemaMapOwners(t, db, table, metadata.ColumnName(e.Fields[0])))
					row, err := db.GetByID(t.Context(), e.Name, rowID, e)
					require.NoError(t, err)
					require.Equal(t, "000123", row[field])
					require.Equal(t, "kept", row["Name"])
					require.Equal(t, indexesBefore, managedActual(t, db, e), "external index definition must be preserved")
					require.Equal(t, registryBefore, managedRegistry(t, db), "external index must not be adopted")
					plan, err = db.PlanMigration(t.Context(), []*metadata.Entity{e}, nil, nil)
					require.NoError(t, err)
					require.Empty(t, plan)
					managedMigrate(t, db, e)
					require.Equal(t, indexesBefore, managedActual(t, db, e))
				})
			})
		}
	}
}
