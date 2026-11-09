package storage_test

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/stretchr/testify/require"
)

// Run with TEST_DATABASE_URL pointing at PostgreSQL 14 as well as 15+.
// Even a catalog without indexes reaches the index introspection in Migrate.
func TestManagedIndexes_ServerVersionCompatibilityMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		e := managedCatalog()
		managedMigrate(t, db, e)
		require.Empty(t, managedRegistry(t, db))
		require.NoError(t, managedPut(t, db, e, map[string]any{"ИНН": "first"}))
		managedMigrate(t, db, e)

		e.Indexes = []metadata.IndexSpec{{Fields: []string{"ИНН"}, Unique: true}}
		managedMigrate(t, db, e)
		registry := managedRegistry(t, db)
		require.NotEmpty(t, registry)
		actual := managedActual(t, db, e)
		managedMigrate(t, db, e)
		require.Equal(t, registry, managedRegistry(t, db))
		require.Equal(t, actual, managedActual(t, db, e))
		require.Error(t, managedPut(t, db, e, map[string]any{"ИНН": "first"}))
		// Ordinary UNIQUE retains SQL's multiple-NULL contract on both engines.
		for range 2 {
			require.NoError(t, managedPut(t, db, e, map[string]any{"ИНН": nil}))
		}
		e.Indexes[0].Unique = false
		managedMigrate(t, db, e)
		require.NoError(t, managedPut(t, db, e, map[string]any{"ИНН": "first"}))
		e.Indexes = nil
		managedMigrate(t, db, e)
		managedMigrate(t, db, e)
		require.Empty(t, managedRegistry(t, db))
		var count int
		require.NoError(t, db.QueryRow(t.Context(), "SELECT COUNT(*) FROM "+metadata.TableName(e.Name)).Scan(&count))
		require.Equal(t, 4, count)
	})
}

func TestManagedIndexes_NullsNotDistinctRemainsExternalMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		if db.IsSQLite() {
			return // This catalog attribute and DDL exist only on PostgreSQL 15+.
		}
		var version int
		require.NoError(t, db.QueryRow(t.Context(), "SELECT current_setting('server_version_num')::integer").Scan(&version))
		if version < 150000 {
			t.Log("NULLS NOT DISTINCT is unavailable before PostgreSQL 15")
			return
		}
		e := managedCatalog()
		managedMigrate(t, db, e)
		e.Indexes = []metadata.IndexSpec{{Fields: []string{"ИНН"}, Unique: true}}
		actions, _, err := db.PlanEntityIndexes(t.Context(), []*metadata.Entity{e})
		require.NoError(t, err)
		var name string
		for _, a := range actions {
			if a.Action == "create" {
				name = a.Index.Name
			}
		}
		require.NotEmpty(t, name)
		e.Indexes = nil
		managedExec(t, db, "CREATE UNIQUE INDEX "+name+" ON "+metadata.TableName(e.Name)+" (инн) NULLS NOT DISTINCT")
		require.NoError(t, managedPut(t, db, e, map[string]any{"ИНН": nil}))
		require.NoError(t, managedPut(t, db, e, map[string]any{"ИНН": "kept"}))
		actions, manifest, err := db.PlanEntityIndexes(t.Context(), []*metadata.Entity{e})
		require.NoError(t, err)
		require.Empty(t, manifest.Indexes)
		var external storage.EntityIndexDefinition
		for _, a := range actions {
			if a.Index.Name == name {
				require.Equal(t, "external", a.Ownership)
				require.Equal(t, "keep", a.Action)
				require.False(t, a.Index.Plain)
				external = a.Index
			}
		}
		require.Equal(t, name, external.Name)
		require.Contains(t, strings.ToUpper(external.Definition), "NULLS NOT DISTINCT")
		before := managedActual(t, db, e)
		var schema string
		require.NoError(t, db.QueryRow(t.Context(), "SELECT current_schema()").Scan(&schema))
		dsn := os.Getenv("TEST_DATABASE_URL")
		if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
			u, err := url.Parse(dsn)
			require.NoError(t, err)
			q := u.Query()
			q.Set("search_path", schema)
			u.RawQuery = q.Encode()
			dsn = u.String()
		} else {
			// dbtest generates schemas containing only ASCII letters/digits/_ .
			dsn += " search_path=" + schema
		}
		require.NoError(t, storage.WithPostgresMigrationPreview(t.Context(), dsn, func(ctx context.Context, preview *storage.DB) error {
			previewActions, previewManifest, err := preview.PlanEntityIndexes(ctx, []*metadata.Entity{e})
			require.NoError(t, err)
			require.Equal(t, actions, previewActions)
			require.Empty(t, previewManifest.Indexes)
			return nil
		}))
		db.SetSchemaOptions(storage.SchemaOptions{AdoptIndexes: []storage.IndexAdoption{{Index: external, SHA256: external.Fingerprint()}}})
		require.Error(t, db.Migrate(t.Context(), []*metadata.Entity{e}))
		db.SetSchemaOptions(storage.SchemaOptions{})
		require.Equal(t, before, managedActual(t, db, e))
		require.Empty(t, managedRegistry(t, db))
		e.Indexes = []metadata.IndexSpec{{Fields: []string{"ИНН"}, Unique: true}}
		require.ErrorContains(t, db.Migrate(t.Context(), []*metadata.Entity{e}), "collision")
		e.Indexes = nil
		managedMigrate(t, db, e)
		require.Equal(t, before, managedActual(t, db, e))
		require.Empty(t, managedRegistry(t, db))
		require.Error(t, managedPut(t, db, e, map[string]any{"ИНН": nil}))
		var count int
		require.NoError(t, db.QueryRow(t.Context(), "SELECT COUNT(*) FROM "+metadata.TableName(e.Name)).Scan(&count))
		require.Equal(t, 2, count)
	})
}
