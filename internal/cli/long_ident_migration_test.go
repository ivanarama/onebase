package cli

import (
	"context"
	"io"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/pflag"
)

// Exercise the real commands: a per-kind storage check cannot see that the
// register's former PostgreSQL name already belongs to a catalog.
func TestMigrationCommandsKeepOtherKindShortTable_Matrix(t *testing.T) {
	for _, command := range []string{"run", "dev", "migrate", "recalc-totals", "deploy"} {
		t.Run(command, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				ctx := context.Background()
				if command == "deploy" && db.Dialect().Name() == "sqlite" {
					t.Skip("deploy supports PostgreSQL only")
				}
				dir := t.TempDir()
				shortName := "Рег_" + strings.Repeat("Я", 28)
				longName := strings.Repeat("Я", 28) + "Продолжение"
				table := metadata.TableName(shortName)
				target := metadata.RegisterTableName(longName)
				writeProcrunFixture(t, dir, "config/app.yaml", "name: long-ident-conflict\n")
				writeProcrunFixture(t, dir, "catalogs/Каталог.yaml", "name: "+shortName+"\nfields:\n  - name: Наименование\n    type: string\n")
				dbArgs := longIdentDatabaseArgs(t, db)
				if err := executeLongIdentCommand(t, append([]string{"migrate", "--project", dir}, dbArgs...)); err != nil {
					t.Fatalf("initial migration: %v", err)
				}
				p := db.Dialect().Placeholder
				if _, err := db.Exec(ctx, "INSERT INTO "+table+" (id, наименование) VALUES ("+p(1)+", "+p(2)+")", uuid.New().String(), "keep catalog data"); err != nil {
					t.Fatal(err)
				}
				before, err := longIdentTableColumns(ctx, db, table)
				if err != nil {
					t.Fatal(err)
				}
				// A pending catalog change also proves the global check precedes
				// the first entity ALTER, rather than just the register migration.
				writeProcrunFixture(t, dir, "catalogs/Каталог.yaml", "name: "+shortName+"\nfields:\n  - name: Наименование\n    type: string\n  - name: НовоеПоле\n    type: string\n")
				writeProcrunFixture(t, dir, "registers/Регистр.yaml", "name: "+longName+"\ntype: balance\nresources:\n  - name: Количество\n    type: number\n")
				args := append([]string{command, "--project", dir}, dbArgs...)
				if command == "run" || command == "dev" {
					args = append(args, "--port", "0")
				}
				err = executeLongIdentCommand(t, args)
				if err == nil || !strings.Contains(err.Error(), "КОНФЛИКТ") {
					t.Errorf("want ownership conflict before migration, got %v", err)
				}
				after, err := longIdentTableColumns(ctx, db, table)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Errorf("catalog schema changed: before=%v after=%v err=%v", before, after, err)
				}
				rows, err := db.Query(ctx, "SELECT наименование FROM "+table)
				if err != nil {
					t.Fatalf("original catalog lost: %v", err)
				}
				defer rows.Close()
				var name string
				if !rows.Next() || rows.Scan(&name) != nil || name != "keep catalog data" {
					t.Errorf("catalog data changed: %q", name)
				}
				if rows.Next() || rows.Err() != nil {
					t.Errorf("unexpected catalog rows: %v", rows.Err())
				}
				if exists, err := db.TableExists(ctx, target); err != nil || exists {
					t.Errorf("register claimed the catalog table: exists=%v err=%v", exists, err)
				}
			})
		})
	}
}

func longIdentDatabaseArgs(t *testing.T, db *storage.DB) []string {
	t.Helper()
	if db.Dialect().Name() == "sqlite" {
		return []string{"--sqlite", db.SQLitePath()}
	}
	rows, err := db.Query(context.Background(), "SELECT current_schema()")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var schema string
	if !rows.Next() || rows.Scan(&schema) != nil || rows.Err() != nil {
		t.Fatal("cannot read matrix PostgreSQL schema")
	}
	dsn := os.Getenv("TEST_DATABASE_URL")
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		dsn = u.String()
	} else {
		dsn += " search_path=" + schema
	}
	return []string{"--db", dsn}
}

func executeLongIdentCommand(t *testing.T, args []string) error {
	t.Helper()
	cmd, _, err := rootCmd.Find(args)
	if err != nil {
		t.Fatal(err)
	}
	var restore []func()
	for _, flags := range []*pflag.FlagSet{rootCmd.PersistentFlags(), cmd.Flags()} {
		flags.VisitAll(func(f *pflag.Flag) {
			value, changed := f.Value.String(), f.Changed
			restore = append(restore, func() {
				if err := f.Value.Set(value); err != nil {
					t.Errorf("restore --%s: %v", f.Name, err)
				}
				f.Changed = changed
			})
		})
	}
	previous := rootCmd.Context()
	previousCommandContext := cmd.Context()
	previousSilenceUsage := rootCmd.SilenceUsage
	defer func() {
		for _, fn := range restore {
			fn()
		}
		rootCmd.SetArgs(nil)
		rootCmd.SetContext(previous)
		cmd.SetContext(previousCommandContext)
		rootCmd.SilenceUsage = previousSilenceUsage
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd.SetContext(ctx)
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
	rootCmd.SetArgs(args)
	return rootCmd.ExecuteContext(ctx)
}

func longIdentTableColumns(ctx context.Context, db *storage.DB, table string) ([]string, error) {
	rows, err := db.Query(ctx, "SELECT * FROM "+table+" LIMIT 0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return rows.FieldNames(), rows.Err()
}
