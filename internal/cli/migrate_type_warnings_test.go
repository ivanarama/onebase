package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/stretchr/testify/require"
)

// Exercise the registered command, including its ordinary and read-only paths.
func TestMigrateWarnsAboutUntrackedPhysicalType(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "types.db")
	writeProcrunFixture(t, dir, "config/app.yaml", "name: types-test\nversion: \"1.0\"\n")
	fixture := func(typ string) {
		writeProcrunFixture(t, dir, "catalogs/Товар.yaml", "name: Товар\nfields:\n  - name: Флаг\n    type: "+typ+"\n")
	}
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		for key, value := range map[string]string{"project": ".", "sqlite": "", "db": "", "dry-run": "false", "allow-destructive": "false", "config-source": "file"} {
			if err := migrateCmd.Flags().Set(key, value); err != nil {
				t.Error(err)
			}
		}
	})
	migrate := func(dry, destructive string) string {
		t.Helper()
		rootCmd.SetArgs([]string{"migrate", "--project", dir, "--sqlite", dbPath, "--dry-run=" + dry, "--allow-destructive=" + destructive, "--config-source=file", "--db="})
		out, err := captureStdout(t, rootCmd.Execute)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	fixture("string")
	if out := migrate("false", "false"); strings.Contains(out, "ПРЕДУПРЕЖДЕНИЕ") {
		t.Fatal(out)
	}
	db, err := storage.ConnectSQLite(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), `INSERT INTO товар (id, флаг) VALUES ('one', 'true')`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	fixture("bool")
	for _, mode := range [][2]string{{"true", "false"}, {"true", "false"}, {"false", "false"}, {"true", "true"}, {"false", "true"}, {"true", "true"}} {
		dry, destructive := mode[0], mode[1]
		out := migrate(dry, destructive)
		for _, want := range []string{"ПРЕДУПРЕЖДЕНИЕ", "товар.флаг", "TEXT", "INTEGER", "без id", "добавление id"} {
			if !strings.Contains(out, want) {
				t.Fatalf("dry-run=%s: missing %q:\n%s", dry, want, out)
			}
		}
		if dry == "true" && !strings.Contains(out, "база не изменена") {
			t.Fatal(out)
		}
	}
	db, err = storage.ConnectSQLite(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var typ, value string
	if err := db.QueryRow(context.Background(), `SELECT type FROM pragma_table_info('товар') WHERE name='флаг'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(context.Background(), `SELECT флаг FROM товар WHERE id='one'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if typ != "TEXT" || value != "true" {
		t.Fatalf("diagnostic changed schema/data: %s %q", typ, value)
	}
}

// Both config sources go through Cobra and reopen the same matrix target.
// Repeated preview and apply must diagnose the mismatch without retyping it.
func TestMigrateTypeWarningsConfigSourceMatrix(t *testing.T) {
	for _, source := range []string{"file", "database"} {
		t.Run(source, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				dir := t.TempDir()
				writeProcrunFixture(t, dir, "config/app.yaml", "name: warning-matrix\n")
				fixture := func(typ string) {
					body := "name: Товар\nfields:\n  - {id: stable, name: Stable, type: string}\n  - {name: Флаг, type: " + typ + "}\n"
					if typ == "bool" {
						body += "  - {name: Новое, type: bool}\n"
					}
					writeProcrunFixture(t, dir, "catalogs/Товар.yaml", body)
				}
				migrate := func(dry, destructive bool, configSource string) (string, error) {
					t.Helper()
					cmd := userReferenceCLICommand(t, db, dir, false)
					for k, v := range map[string]string{"dry-run": fmt.Sprint(dry), "allow-destructive": fmt.Sprint(destructive), "config-source": configSource} {
						require.NoError(t, cmd.Flags().Set(k, v))
					}
					cmd.Use, cmd.RunE = "migrate", migrateCmd.RunE
					cmd.SilenceErrors, cmd.SilenceUsage = true, true
					return captureStdout(t, func() error { return cmd.ExecuteContext(t.Context()) })
				}
				fixture("string")
				out, err := migrate(false, false, "file")
				require.NoError(t, err)
				require.NotContains(t, out, "ПРЕДУПРЕЖДЕНИЕ")
				id := "00000000-0000-0000-0000-000000000001"
				_, err = db.Exec(t.Context(), fmt.Sprintf("INSERT INTO товар (id, флаг) VALUES (%s,%s)", db.Dialect().Placeholder(1), db.Dialect().Placeholder(2)), id, "true")
				require.NoError(t, err)
				fixture("bool")
				if source == "database" {
					repo := configdb.New(db)
					require.NoError(t, repo.EnsureSchema(t.Context()))
					require.NoError(t, repo.ImportFromDir(t.Context(), dir))
				}
				prepareSQLiteCLIPreview(t, db)
				for _, mode := range [][2]bool{{true, false}, {true, false}, {false, false}, {true, true}, {false, true}, {true, true}} {
					var before map[string]string
					if db.IsSQLite() && mode[0] {
						before = previewTree(t, filepath.Dir(db.SQLitePath()))
					}
					out, err = migrate(mode[0], mode[1], source)
					require.NoError(t, err)
					require.Equal(t, 1, strings.Count(out, "ПРЕДУПРЕЖДЕНИЕ"), out)
					require.Contains(t, out, "товар.флаг")
					require.Contains(t, out, "без id")
					require.NotContains(t, out, "товар.новое")
					if mode[0] {
						require.Contains(t, out, "база не изменена")
						if db.IsSQLite() {
							require.Equal(t, before, previewTree(t, filepath.Dir(db.SQLitePath())))
						}
					}
					var typ, value string
					q := "SELECT type FROM pragma_table_info('товар') WHERE name='флаг'"
					if !db.IsSQLite() {
						q = "SELECT data_type FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='товар' AND column_name='флаг'"
					}
					require.NoError(t, db.QueryRow(t.Context(), q).Scan(&typ))
					require.Equal(t, "text", strings.ToLower(typ))
					require.NoError(t, db.QueryRow(t.Context(), "SELECT флаг FROM товар").Scan(&value))
					require.Equal(t, "true", value)
					prepareSQLiteCLIPreview(t, db)
				}
				// Planning now fails after the mismatch was diagnosed. No
				// warning or success text may escape the preview buffer.
				_, err = db.Exec(t.Context(), "DROP TABLE _schema_fields")
				require.NoError(t, err)
				_, err = db.Exec(t.Context(), "CREATE TABLE _schema_fields (broken TEXT)")
				require.NoError(t, err)
				prepareSQLiteCLIPreview(t, db)
				var before map[string]string
				if db.IsSQLite() {
					before = previewTree(t, filepath.Dir(db.SQLitePath()))
				}
				out, err = migrate(true, false, source)
				require.ErrorContains(t, err, "schema map: read")
				require.Empty(t, out, "failed catalog read must discard buffered warnings")
				if db.IsSQLite() {
					require.Equal(t, before, previewTree(t, filepath.Dir(db.SQLitePath())))
				}
			})
		})
	}
}
