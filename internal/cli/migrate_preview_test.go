package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// Execute the command's public RunE with Cobra's lifecycle and context; this
// exercises the opener, config loader and planner together, not a private guard.
func executeMigration(t *testing.T, dir, path string, flags map[string]string) (string, error) {
	t.Helper()
	cmd := migrateCmdFor(t, dir, path, flags)
	cmd.Use = "migrate"
	cmd.RunE = runMigrate
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	return captureStdout(t, func() error { return cmd.ExecuteContext(t.Context()) })
}
func previewTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer func() { require.NoError(t, root.Close()) }()
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			out["absent"] = ""
			return nil
		}
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			out[rel] = "directory"
			return nil
		}
		b, err := root.ReadFile(rel)
		if err != nil {
			return err
		}
		out[rel] = string(b)
		return nil
	})
	require.NoError(t, err)
	return out
}
func previewLock(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path+".onebase.lock", []byte("existing lifetime lock\n"), 0600))
}
func previewRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}
func previewSQL(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), q, args...)
	require.NoError(t, err)
}

func TestMigratePreview_FileSnapshots(t *testing.T) {
	for _, state := range []string{"old", "empty", "missing", "missing-parent", "current"} {
		for _, source := range []string{"file", "database"} {
			t.Run(state+"/"+source, func(t *testing.T) {
				dir, dbDir := t.TempDir(), t.TempDir()
				path := filepath.Join(dbDir, "preview.db")
				migratePlanFixture(t, dir, false)
				switch state {
				case "empty":
					require.NoError(t, os.WriteFile(path, nil, 0600))
					previewLock(t, path)
				case "old":
					db := previewRaw(t, path)
					previewSQL(t, db, "CREATE TABLE _settings(key TEXT PRIMARY KEY,value TEXT NOT NULL)")
					previewSQL(t, db, "INSERT INTO _settings VALUES ('preserved','data')")
					if source == "database" {
						previewSQL(t, db, "CREATE TABLE _onebase_config(path TEXT PRIMARY KEY,content BLOB NOT NULL)")
						for _, rel := range []string{"config/app.yaml", "catalogs/Товар.yaml"} {
							b, err := os.ReadFile(filepath.Join(dir, rel))
							require.NoError(t, err)
							previewSQL(t, db, "INSERT INTO _onebase_config VALUES (?,?)", rel, b)
						}
					}
					require.NoError(t, db.Close())
					previewLock(t, path)
				case "current":
					_, err := executeMigration(t, dir, path, nil)
					require.NoError(t, err)
					if source == "database" {
						db, err := openCLIStorage(t.Context(), "sqlite", path, "")
						require.NoError(t, err)
						repo := configdb.New(db)
						require.NoError(t, repo.EnsureSchema(t.Context()))
						require.NoError(t, repo.ImportFromDir(t.Context(), dir))
						db.Close()
					}
				case "missing-parent":
					path = filepath.Join(dbDir, "not-created", "deep", "preview.db")
				}
				before := previewTree(t, dbDir)
				out, err := executeMigration(t, dir, path, map[string]string{"dry-run": "true", "config-source": source})
				if source == "database" && (state == "empty" || state == "missing" || state == "missing-parent") {
					require.ErrorContains(t, err, "_onebase_config")
					require.Empty(t, out)
				} else {
					require.NoError(t, err)
					require.Contains(t, out, "Реструктуризация")
				}
				require.Equal(t, before, previewTree(t, dbDir), "preview must preserve every DB/sidecar/lock byte and directory")
			})
		}
	}
}

func TestMigratePreview_RefusesUnsafeSQLiteWithoutWriting(t *testing.T) {
	for _, state := range []string{"no-lock", "wal", "shm", "journal", "pending-restore", "newer-revision"} {
		t.Run(state, func(t *testing.T) {
			dir, dbDir := t.TempDir(), t.TempDir()
			path := filepath.Join(dbDir, "preview.db")
			migratePlanFixture(t, dir, false)
			db := previewRaw(t, path)
			previewSQL(t, db, "CREATE TABLE sentinel(value TEXT)")
			if state == "pending-restore" {
				previewSQL(t, db, "CREATE TABLE _settings(key TEXT PRIMARY KEY,value TEXT)")
				previewSQL(t, db, "INSERT INTO _settings VALUES ('onebase.internal.restore.intent.v1','{}')")
			}
			if state == "newer-revision" {
				previewSQL(t, db, "CREATE TABLE _schema_revision(id INTEGER PRIMARY KEY,revision INTEGER,updated_by TEXT)")
				previewSQL(t, db, "INSERT INTO _schema_revision VALUES (1,?, 'future')", storage.SchemaRevision+1)
			}
			require.NoError(t, db.Close())
			if state != "no-lock" {
				previewLock(t, path)
			}
			if state == "wal" || state == "shm" || state == "journal" {
				require.NoError(t, os.WriteFile(path+"-"+state, []byte("unrecovered sidecar"), 0600))
			}
			before := previewTree(t, dbDir)
			out, err := executeMigration(t, dir, path, map[string]string{"dry-run": "true"})
			require.Error(t, err)
			require.Empty(t, out)
			require.Equal(t, before, previewTree(t, dbDir))
		})
	}
}

func TestMigratePreview_HotWALMarker(t *testing.T) {
	dir, dbDir := t.TempDir(), t.TempDir()
	path := filepath.Join(dbDir, "hot.db")
	migratePlanFixture(t, dir, false)
	db := previewRaw(t, path)
	previewSQL(t, db, "PRAGMA journal_mode=WAL")
	previewSQL(t, db, "PRAGMA wal_autocheckpoint=0")
	previewSQL(t, db, "CREATE TABLE _settings(key TEXT PRIMARY KEY,value TEXT)")
	previewSQL(t, db, "INSERT INTO _settings VALUES ('onebase.internal.restore.intent.v1','{}')")
	previewLock(t, path)
	before := previewTree(t, dbDir)
	out, err := executeMigration(t, dir, path, map[string]string{"dry-run": "true"})
	require.ErrorContains(t, err, "sidecar")
	require.Empty(t, out)
	require.Equal(t, before, previewTree(t, dbDir))
	// Recovery/marker cleanup is explicitly outside preview. Closing the writer
	// checkpoints its WAL; normal SQLite operations remove the pending marker.
	previewSQL(t, db, "DELETE FROM _settings WHERE key='onebase.internal.restore.intent.v1'")
	require.NoError(t, db.Close())
	before = previewTree(t, dbDir)
	out, err = executeMigration(t, dir, path, map[string]string{"dry-run": "true"})
	require.NoError(t, err)
	require.NotEmpty(t, out)
	require.Equal(t, before, previewTree(t, dbDir))
}

func TestMigratePreview_LegacyManifestAndApply(t *testing.T) {
	dir, dbDir := t.TempDir(), t.TempDir()
	path := filepath.Join(dbDir, "legacy.db")
	migratePlanFixture(t, dir, false)
	_, err := executeMigration(t, dir, path, nil)
	require.NoError(t, err)
	proj, err := project.Load(dir)
	require.NoError(t, err)
	defer proj.Close()
	e := proj.Entities[0]
	db, err := openCLIStorage(t.Context(), "sqlite", path, "")
	require.NoError(t, err)
	_, err = db.Exec(t.Context(), storage.CreateEntityIndexSQL(metadata.TableName(e.Name), []string{"наименование"}, true))
	require.NoError(t, err)
	db.Close()
	before := previewTree(t, dbDir)
	out, err := executeMigration(t, dir, path, map[string]string{"dry-run": "true"})
	require.NoError(t, err)
	require.Contains(t, out, "blocked-legacy")
	require.Equal(t, before, previewTree(t, dbDir))
	start := strings.Index(out, "{\n")
	require.GreaterOrEqual(t, start, 0)
	dec := json.NewDecoder(strings.NewReader(out[start:]))
	var manifest storage.IndexManifest
	require.NoError(t, dec.Decode(&manifest))
	require.Len(t, manifest.Indexes, 1)
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	manifestPath := filepath.Join(t.TempDir(), "adopt.json")
	require.NoError(t, os.WriteFile(manifestPath, raw, 0600))
	_, err = executeMigration(t, dir, path, nil)
	require.ErrorContains(t, err, "legacy UNIQUE retained")
	cmd := migrateCmdFor(t, dir, path, nil)
	cmd.Flags().String("adopt-indexes", manifestPath, "")
	cmd.Use = "migrate"
	cmd.RunE = runMigrate
	_, err = captureStdout(t, func() error { return cmd.ExecuteContext(t.Context()) })
	require.NoError(t, err)
	out, err = executeMigration(t, dir, path, map[string]string{"dry-run": "true"})
	require.NoError(t, err)
	require.NotContains(t, out, "blocked-legacy")
	require.NotContains(t, out, "adopt-indexes")
}

func TestMigratePreview_PostgresRoleWithoutDDL(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close(ctx)) })
	schema := storage.NewEphemeralSchemaName()
	role := schema + "_reader"
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	require.NoError(t, err)
	_, err = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize())
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		require.NoError(t, err)
		_, err = admin.Exec(ctx, "DROP ROLE "+pgx.Identifier{role}.Sanitize())
		require.NoError(t, err)
	})
	// The role can read a pre-existing catalog/config but cannot create tables.
	_, err = admin.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s._onebase_config(path TEXT PRIMARY KEY,content BYTEA NOT NULL); GRANT USAGE ON SCHEMA %s TO %s; GRANT SELECT ON %s._onebase_config TO %s`, schema, schema, role, schema, role))
	require.NoError(t, err)
	dir := t.TempDir()
	migratePlanFixture(t, dir, false)
	for _, rel := range []string{"config/app.yaml", "catalogs/Товар.yaml"} {
		b, err := os.ReadFile(filepath.Join(dir, rel))
		require.NoError(t, err)
		_, err = admin.Exec(ctx, "INSERT INTO "+schema+"._onebase_config VALUES ($1,$2)", rel, b)
		require.NoError(t, err)
	}
	cfg, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	// Startup role and search_path exercise the actual read-only opener rather
	// than SET ROLE on a different connection with broader permissions.
	cfg.RuntimeParams["role"] = role
	cfg.RuntimeParams["search_path"] = schema
	readerDSN := cfg.ConnString()
	// ConnString keeps the original DSN; append parameters with pgx's keyword
	// quoting since this controlled role/schema contains only ASCII identifiers.
	readerDSN += " role=" + role + " search_path=" + schema
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		separator := "?"
		if strings.Contains(dsn, "?") {
			separator = "&"
		}
		readerDSN = dsn + separator + "role=" + role + "&search_path=" + schema
	}
	for _, source := range []string{"file", "database"} {
		out, err := executeMigration(t, dir, "", map[string]string{"db": readerDSN, "dry-run": "true", "config-source": source})
		require.NoError(t, err)
		require.Contains(t, out, "Реструктуризация")
	}
	var tables int
	require.NoError(t, admin.QueryRow(ctx, "SELECT COUNT(*) FROM pg_tables WHERE schemaname=$1", schema).Scan(&tables))
	require.Equal(t, 1, tables)
	require.Error(t, storage.WithPostgresMigrationPreview(ctx, readerDSN, func(ctx context.Context, db *storage.DB) error {
		_, err := db.Exec(ctx, "CREATE TABLE forbidden(value TEXT)")
		require.Error(t, err)
		return err
	}), "writes must be rejected")
}

func TestMigratePreview_MissingTargetAppears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.db")
	err := storage.WithSQLiteMigrationPreview(t.Context(), path, false, func(context.Context, *storage.DB) error {
		return os.WriteFile(path, []byte("concurrent creator"), 0600)
	})
	require.ErrorContains(t, err, "changed during preview")
}

func TestMigratePreview_CatalogChangesDuringRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db := previewRaw(t, path)
	previewSQL(t, db, "CREATE TABLE sentinel(value TEXT)")
	require.NoError(t, db.Close())
	err := storage.WithSQLiteMigrationPreview(t.Context(), path, true, func(context.Context, *storage.DB) error {
		// Simulate an uncooperative writer bypassing the lifetime lease.
		changed := previewRaw(t, path)
		previewSQL(t, changed, "INSERT INTO sentinel VALUES ('concurrent')")
		return changed.Close()
	})
	require.ErrorContains(t, err, "changed during preview")
}

func TestMigratePreview_SQLiteRejectsWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db := previewRaw(t, path)
	previewSQL(t, db, "CREATE TABLE sentinel(value TEXT)")
	require.NoError(t, db.Close())
	before := previewTree(t, filepath.Dir(path))
	err := storage.WithSQLiteMigrationPreview(t.Context(), path, true, func(ctx context.Context, db *storage.DB) error {
		_, err := db.Exec(ctx, "INSERT INTO sentinel VALUES ('forbidden')")
		return err
	})
	require.Error(t, err)
	require.Equal(t, before, previewTree(t, filepath.Dir(path)))
}
