package storage_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/stretchr/testify/require"
)

// Fixtures model an older binary's exact signed ownership, including the
// actual truncated PostgreSQL identifiers. Production never rewrites records.
func legacyManagedFixture(t *testing.T, db *storage.DB, column bool) (*metadata.Entity, string, string, storage.EntityIndexDefinition) {
	t.Helper()
	e := &metadata.Entity{Name: "Identity" + strings.Repeat("a", 70), Kind: metadata.KindCatalog,
		Fields:  []metadata.Field{{ID: "value", Name: "Value", Type: metadata.FieldTypeString}},
		Indexes: []metadata.IndexSpec{{Fields: []string{"Value"}, Unique: true}}}
	if column {
		e.Name = "IdentityColumn"
		e.Fields[0].Name = "Value" + strings.Repeat("b", 70)
		e.Indexes[0].Fields[0] = e.Fields[0].Name
	}
	managedMigrate(t, db, e)
	require.NoError(t, managedPut(t, db, e, map[string]any{e.Fields[0].Name: "kept"}))
	actions, _, err := db.PlanEntityIndexes(t.Context(), []*metadata.Entity{e})
	require.NoError(t, err)
	var managed []storage.EntityIndexDefinition
	for _, action := range actions {
		if action.Ownership == "managed" {
			managed = append(managed, action.Index)
		}
	}
	require.Len(t, managed, 1)
	index := managed[0]
	table, col := metadata.TableName(e.Name), metadata.ColumnName(e.Fields[0])
	old := metadata.TableLogical(e.Name)
	if column {
		old = metadata.LogicalColumnName(e.Fields[0])
	}
	if db.IsPostgres() {
		old = metadata.LegacySQLIdents(old)[1]
	}
	if column {
		managedExec(t, db, "ALTER TABLE "+table+" RENAME COLUMN "+col+" TO "+old)
		managedExec(t, db, "UPDATE _schema_fields SET column_name="+db.Dialect().Placeholder(1)+" WHERE table_name="+db.Dialect().Placeholder(2)+" AND field_id='value'", old, table)
		col = old
		index.Columns = []string{col}
	} else {
		managedExec(t, db, "ALTER TABLE "+table+" RENAME TO "+old)
		managedExec(t, db, "UPDATE _schema_fields SET table_name="+db.Dialect().Placeholder(1)+" WHERE table_name="+db.Dialect().Placeholder(2), old, table)
		table = old
		index.Table = table
	}
	// Older binaries generated the index name from the old physical identity.
	managedExec(t, db, "DROP INDEX "+index.Name)
	ddl := storage.CreateEntityIndexSQL(table, index.Columns, true)
	managedExec(t, db, ddl)
	index.Name = strings.Fields(ddl)[6]
	q := "SELECT sql FROM sqlite_schema WHERE type='index' AND name=?"
	if db.IsPostgres() {
		q = "SELECT indexdef FROM pg_indexes WHERE schemaname=current_schema() AND indexname=$1"
	}
	require.NoError(t, db.QueryRow(t.Context(), q, index.Name).Scan(&index.Definition))
	raw, err := json.Marshal(index)
	require.NoError(t, err)
	managedExec(t, db, "DELETE FROM _managed_entity_indexes")
	p := db.Dialect().Placeholder
	managedExec(t, db, "INSERT INTO _managed_entity_indexes VALUES ("+p(1)+","+p(2)+",1,"+p(3)+","+p(4)+",'created')", table, index.Name, string(raw), index.Fingerprint())
	// An unrelated external index is also part of the preservation snapshot.
	managedExec(t, db, "CREATE INDEX identity_external ON "+table+" ("+col+")")
	return e, table, col, index
}

func TestManagedIndexes_LegacyIdentityRefusalMatrix(t *testing.T) {
	for _, kind := range []string{"table", "column", "already-renamed-table", "already-renamed-column"} {
		t.Run(kind, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				e, table, col, index := legacyManagedFixture(t, db, strings.Contains(kind, "column"))
				if strings.HasPrefix(kind, "already-renamed") {
					if strings.Contains(kind, "column") {
						managedExec(t, db, "ALTER TABLE "+table+" RENAME COLUMN "+col+" TO "+metadata.ColumnName(e.Fields[0]))
						col = metadata.ColumnName(e.Fields[0])
					} else {
						managedExec(t, db, "ALTER TABLE "+table+" RENAME TO "+metadata.TableName(e.Name))
						table = metadata.TableName(e.Name)
					}
				}
				before := currentLegacySchemaSnapshot(t, db)
				registry := managedRegistry(t, db)
				e.Indexes = nil // Never silently classify the still-enforced UNIQUE as external.
				for _, opts := range []storage.SchemaOptions{{}, {AllowDestructive: true}, {AdoptIndexes: []storage.IndexAdoption{{Index: index, SHA256: index.Fingerprint()}}}} {
					db.SetSchemaOptions(opts)
					for range 2 {
						err := db.Migrate(t.Context(), []*metadata.Entity{e})
						require.ErrorContains(t, err, "ownership transfer is unsupported")
						require.Contains(t, err.Error(), index.Name)
						require.Equal(t, before, currentLegacySchemaSnapshot(t, db))
						require.Equal(t, registry, managedRegistry(t, db))
						var value string
						require.NoError(t, db.QueryRow(t.Context(), "SELECT "+col+" FROM "+table).Scan(&value))
						require.Equal(t, "kept", value)
						p := db.Dialect().Placeholder
						_, err = db.Exec(t.Context(), "INSERT INTO "+table+" (id,"+col+") VALUES ("+p(1)+","+p(2)+")", uuid.New().String(), "kept")
						require.Error(t, err, "UNIQUE must remain enforced after refusal")
					}
				}
				_, _, err := db.PlanEntityIndexes(t.Context(), []*metadata.Entity{e})
				require.ErrorContains(t, err, "ownership transfer is unsupported")
			})
		})
	}
}

func TestManagedIndexes_LegacyIdentityControlsMatrix(t *testing.T) {
	for _, state := range []string{"missing-registry", "empty-registry", "unrelated-registry", "current-managed"} {
		t.Run(state, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				e := &metadata.Entity{Name: "Control" + strings.Repeat("a", 70), Kind: metadata.KindCatalog, Fields: []metadata.Field{{ID: "value", Name: "Value", Type: metadata.FieldTypeString}}}
				if state == "current-managed" {
					e.Indexes = []metadata.IndexSpec{{Fields: []string{"Value"}, Unique: true}}
					managedMigrate(t, db, e)
					require.NoError(t, managedPut(t, db, e, map[string]any{"Value": "kept"}))
					require.Error(t, managedPut(t, db, e, map[string]any{"Value": "kept"}))
					e.Indexes[0].Unique = false
					managedMigrate(t, db, e)
					require.NoError(t, managedPut(t, db, e, map[string]any{"Value": "kept"}))
					e.Indexes = nil
					managedMigrate(t, db, e)
					require.NoError(t, managedPut(t, db, e, map[string]any{"Value": "kept"}))
					return
				}
				if state == "empty-registry" {
					require.NoError(t, db.EnsureManagedIndexSchema(t.Context()))
				}
				if state == "unrelated-registry" {
					other := managedCatalog()
					other.Indexes = []metadata.IndexSpec{{Fields: []string{"ИНН"}, Unique: true}}
					managedMigrate(t, db, other)
				}
				old := metadata.TableLogical(e.Name)
				if db.IsPostgres() {
					old = metadata.LegacySQLIdents(old)[1]
				}
				managedExec(t, db, fmt.Sprintf("CREATE TABLE %s (id %s PRIMARY KEY,value TEXT)", old, db.Dialect().TypeUUID()))
				managedExec(t, db, "CREATE INDEX control_external ON "+old+" (value)")
				p := db.Dialect().Placeholder
				id := uuid.New()
				managedExec(t, db, "INSERT INTO "+old+" (id,value) VALUES ("+p(1)+","+p(2)+")", id.String(), "kept")
				managedMigrate(t, db, e)
				managedMigrate(t, db, e)
				row, err := db.GetByID(t.Context(), e.Name, id, e)
				require.NoError(t, err)
				require.Equal(t, "kept", row["Value"])
				require.NoError(t, managedPut(t, db, e, map[string]any{"Value": "kept"}))
			})
		})
	}
}

func TestManagedIndexes_IdentityRecheckBeforeStructuralDDLMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		parent, child := managedCatalog(), managedCatalog()
		child.Fields = append(child.Fields, metadata.Field{ID: "owner", Name: "Owner", Type: metadata.FieldType("reference"), RefEntity: parent.Name})
		child.Indexes = []metadata.IndexSpec{{Fields: []string{"ИНН"}, Unique: true}}
		managedMigrate(t, db, parent, child)
		require.NoError(t, managedPut(t, db, child, map[string]any{"ИНН": "kept"}))
		actions, _, err := db.PlanEntityIndexes(t.Context(), []*metadata.Entity{child})
		require.NoError(t, err)
		var index storage.EntityIndexDefinition
		for _, a := range actions {
			if a.Ownership == "managed" {
				index = a.Index
			}
		}
		parent.Fields = append(parent.Fields, metadata.Field{ID: "extra", Name: "Extra", Type: metadata.FieldTypeString})
		child.Fields = append(child.Fields, metadata.Field{ID: "extra", Name: "Extra", Type: metadata.FieldTypeString})
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
		childSnapshot := func() []string {
			var out []string
			for _, line := range currentLegacySchemaSnapshot(t, db) {
				if strings.Contains(line, index.Table) && !strings.Contains(line, index.Name) {
					out = append(out, line)
				}
			}
			return out
		}
		snapshot := childSnapshot()
		injected := false
		registry := managedRegistry(t, db)
		db.SetSchemaOptions(storage.SchemaOptions{Report: func(c storage.SchemaChange, applied bool) {
			if !injected && applied && c.Table == metadata.TableName(parent.Name) {
				injected = true
				require.NoError(t, externalExec("DROP INDEX "+index.Name))
				require.NoError(t, externalExec("CREATE INDEX "+index.Name+" ON "+index.Table+" (инн)"))
			}
		}})
		err = db.Migrate(t.Context(), []*metadata.Entity{parent, child})
		require.True(t, injected)
		require.ErrorContains(t, err, "managed index differs from catalog")
		// Parent's committed change is allowed; the refused child and its catalog
		// remain exactly as the external writer left them, without registry repair.
		require.Equal(t, snapshot, childSnapshot())
		require.Equal(t, registry, managedRegistry(t, db))
		var value string
		require.NoError(t, db.QueryRow(t.Context(), "SELECT инн FROM "+index.Table).Scan(&value))
		require.Equal(t, "kept", value)
	})
}

func TestManagedIndexes_LegacyColumnWithoutOwnershipMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		e := &metadata.Entity{Name: "LegacyExternal", Kind: metadata.KindCatalog, Fields: []metadata.Field{{ID: "value", Name: "Value" + strings.Repeat("b", 70), Type: metadata.FieldTypeString}}}
		table, old := metadata.TableName(e.Name), metadata.LogicalColumnName(e.Fields[0])
		if db.IsPostgres() {
			old = metadata.LegacySQLIdents(old)[1]
		}
		managedExec(t, db, "CREATE TABLE "+table+" (id "+db.Dialect().TypeUUID()+" PRIMARY KEY,"+old+" TEXT)")
		managedExec(t, db, "CREATE INDEX legacy_external ON "+table+" ("+old+") WHERE "+old+" IS NOT NULL")
		require.NoError(t, db.EnsureSchemaMapSchema(t.Context()))
		p := db.Dialect().Placeholder
		managedExec(t, db, "INSERT INTO _schema_fields VALUES ("+p(1)+",'value',"+p(2)+",'string')", table, old)
		id := uuid.New()
		managedExec(t, db, "INSERT INTO "+table+" (id,"+old+") VALUES ("+p(1)+",'kept')", id.String())
		for range 2 {
			managedMigrate(t, db, e)
		}
		row, err := db.GetByID(t.Context(), e.Name, id, e)
		require.NoError(t, err)
		require.Equal(t, "kept", row[e.Fields[0].Name])
		actions, manifest, err := db.PlanEntityIndexes(t.Context(), []*metadata.Entity{e})
		require.NoError(t, err)
		require.Empty(t, manifest.Indexes)
		found := false
		for _, a := range actions {
			if a.Index.Name == "legacy_external" {
				found = true
				require.Equal(t, "external", a.Ownership)
				require.Contains(t, a.Index.Definition, metadata.ColumnName(e.Fields[0]))
			}
		}
		require.True(t, found)
		require.Empty(t, managedRegistry(t, db))
	})
}
