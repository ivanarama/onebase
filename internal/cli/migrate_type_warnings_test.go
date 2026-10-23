package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/storage"
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
