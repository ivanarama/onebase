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

// An old SQLite database can contain both full and prefix names. The public
// dry-run must show the same early conflict as the real migration.
func TestMigrateDryRunFullLegacyBesideShortOwner(t *testing.T) {
	for _, kind := range []string{"column", "table"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			path := dir + "/legacy.db"
			db, err := storage.ConnectSQLite(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			short := strings.Repeat("Я", 31)
			full := short + "Продолжение"
			writeProcrunFixture(t, dir, "config/app.yaml", "name: legacy-preflight\n")
			tables := []string{"владелец"}
			if kind == "column" {
				writeProcrunFixture(t, dir, "catalogs/Владелец.yaml", "name: Владелец\nfields:\n  - name: "+short+"\n    type: string\n  - name: "+full+"\n    type: string\n")
				if _, err := db.Exec(ctx, "CREATE TABLE владелец (id TEXT PRIMARY KEY, "+strings.ToLower(short)+" TEXT, "+strings.ToLower(full)+" TEXT)"); err != nil {
					t.Fatal(err)
				}
			} else {
				tables = []string{strings.ToLower(short), strings.ToLower(full)}
				for i, name := range []string{short, full} {
					writeProcrunFixture(t, dir, "catalogs/"+name+".yaml", "name: "+name+"\nfields:\n  - name: Наименование\n    type: string\n")
					if _, err := db.Exec(ctx, "CREATE TABLE "+tables[i]+" (id TEXT PRIMARY KEY, наименование TEXT)"); err != nil {
						t.Fatal(err)
					}
				}
			}
			prepareSQLiteCLIPreview(t, db)
			before := make([][]string, len(tables))
			for i, table := range tables {
				before[i], err = longIdentTableColumns(ctx, db, table)
				if err != nil {
					t.Fatal(err)
				}
			}
			for attempt := 0; attempt < 2; attempt++ {
				prepareSQLiteCLIPreview(t, db)
				out, err := captureStdout(t, func() error {
					return executeLongIdentCommand(t, []string{"migrate", "--project", dir, "--sqlite", path, "--dry-run"})
				})
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out, "КОНФЛИКТ") || !strings.Contains(out, "претендуют 2") {
					t.Errorf("dry-run concealed owner conflict: %s", out)
				}
				err = executeLongIdentCommand(t, []string{"migrate", "--project", dir, "--sqlite", path})
				if err == nil || !strings.Contains(err.Error(), "КОНФЛИКТ") {
					t.Errorf("migration did not refuse: %v", err)
				}
				for i, table := range tables {
					after, err := longIdentTableColumns(ctx, db, table)
					if err != nil || !reflect.DeepEqual(before[i], after) {
						t.Errorf("original schema changed: %s before %v after %v err %v", table, before[i], after, err)
					}
				}
			}
		})
	}
}

func TestMigrateDryRunLegacyUpgradeIsIdempotent(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		dir := t.TempDir()
		name := "СправочникСОченьДлиннымИменемИзКонфигурации1С"
		field := "РеквизитСОченьДлиннымИменемКоторыйПришёлИзОдинЭс"
		writeProcrunFixture(t, dir, "config/app.yaml", "name: legacy-upgrade\n")
		writeProcrunFixture(t, dir, "catalogs/Каталог.yaml", "name: "+name+"\nfields:\n  - name: "+field+"\n    type: string\n")
		oldTable, oldCol := strings.ToLower(name), strings.ToLower(field)
		if !db.IsSQLite() {
			oldTable = metadata.LegacySQLIdents(oldTable)[1]
			oldCol = metadata.LegacySQLIdents(oldCol)[1]
		}
		d := db.Dialect()
		id := uuid.New()
		if _, err := db.Exec(ctx, "CREATE TABLE "+oldTable+" (id "+d.TypeUUID()+" PRIMARY KEY, "+oldCol+" TEXT)"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, "INSERT INTO "+oldTable+" (id, "+oldCol+") VALUES ("+d.Placeholder(1)+", 'original')", id.String()); err != nil {
			t.Fatal(err)
		}
		args := append([]string{"migrate", "--project", dir}, longIdentDatabaseArgs(t, db)...)
		for attempt := 0; attempt < 2; attempt++ {
			if err := executeLongIdentCommand(t, args); err != nil {
				t.Fatal(err)
			}
			prepareSQLiteCLIPreview(t, db)
			out, err := captureStdout(t, func() error { return executeLongIdentCommand(t, append(append([]string{}, args...), "--dry-run")) })
			if err != nil || strings.Contains(out, "Длинные имена (#1946") {
				t.Fatalf("successful upgrade must have no pending renames: %s %v", out, err)
			}
			entity := &metadata.Entity{Name: name, Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: field, Type: metadata.FieldTypeString}}}
			row, err := db.GetByID(ctx, name, id, entity)
			if err != nil || row[field] != "original" {
				t.Fatalf("upgrade lost data: %v %v", row, err)
			}
		}
	})
}

func TestMigrateDryRunCurrentLegacyBeforeDDL_Matrix(t *testing.T) {
	for _, kind := range []string{"table", "column"} {
		for _, state := range []string{"empty", "short-owner", "both-modern"} {
			t.Run(kind+"/"+state, func(t *testing.T) {
				dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
					ctx := context.Background()
					dir := t.TempDir()
					s, l := strings.Repeat("Я", 31), strings.Repeat("Я", 31)+"Продолжение"
					writeProcrunFixture(t, dir, "config/app.yaml", "name: current-legacy-preflight\n")
					short := &metadata.Entity{Name: s, Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
					long := &metadata.Entity{Name: l, Kind: metadata.KindCatalog, Fields: short.Fields}
					if kind == "table" {
						for i, name := range []string{s, l} {
							writeProcrunFixture(t, dir, "catalogs/"+[]string{"Короткий", "Длинный"}[i]+".yaml", "name: "+name+"\nfields:\n  - name: Наименование\n    type: string\n")
						}
					} else {
						short.Name = "Владелец"
						short.Fields = []metadata.Field{{Name: s, Type: metadata.FieldTypeString}}
						writeProcrunFixture(t, dir, "catalogs/Владелец.yaml", "name: Владелец\nfields:\n  - name: "+s+"\n    type: string\n  - name: "+l+"\n    type: string\n")
					}
					independent := &metadata.Entity{Name: "Независимый", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Значение", Type: metadata.FieldTypeString, ID: "keep"}}}
					if err := db.Migrate(ctx, []*metadata.Entity{independent}); err != nil {
						t.Fatal(err)
					}
					id := uuid.New()
					if err := db.Upsert(ctx, independent.Name, id, map[string]any{"Значение": "keep"}, independent); err != nil {
						t.Fatal(err)
					}
					writeProcrunFixture(t, dir, "catalogs/Независимый.yaml", "name: Независимый\nfields:\n  - name: Значение\n    type: string\n    id: keep\n  - name: НовоеПоле\n    type: string\n    id: pending\n")
					if state == "both-modern" && kind == "table" {
						if err := db.Migrate(ctx, []*metadata.Entity{long}); err != nil {
							t.Fatal(err)
						}
						if err := db.Upsert(ctx, long.Name, id, map[string]any{"Наименование": "long data"}, long); err != nil {
							t.Fatal(err)
						}
					}
					if state != "empty" {
						if err := db.Migrate(ctx, []*metadata.Entity{short}); err != nil {
							t.Fatal(err)
						}
						if err := db.Upsert(ctx, short.Name, id, map[string]any{short.Fields[0].Name: "short data"}, short); err != nil {
							t.Fatal(err)
						}
					}
					if state == "both-modern" {
						if kind == "column" {
							col := metadata.ColumnName(metadata.Field{Name: l, Type: metadata.FieldTypeString})
							if _, err := db.Exec(ctx, "ALTER TABLE владелец ADD COLUMN "+col+" TEXT"); err != nil {
								t.Fatal(err)
							}
							if _, err := db.Exec(ctx, "UPDATE владелец SET "+col+" = 'long data'"); err != nil {
								t.Fatal(err)
							}
						}
					}
					// Finish only the fixture's service revision; the conflicting
					// application metadata must remain unapplied for the preflight.
					if _, err := db.RaiseSchemaRevision(ctx); err != nil {
						t.Fatal(err)
					}
					prepareSQLiteCLIPreview(t, db)
					before := longIdentSchemaSnapshot(t, db)
					args := append([]string{"migrate", "--project", dir}, longIdentDatabaseArgs(t, db)...)
					var previous string
					for attempt := 0; attempt < 2; attempt++ {
						prepareSQLiteCLIPreview(t, db)
						out, err := captureStdout(t, func() error { return executeLongIdentCommand(t, append(append([]string{}, args...), "--dry-run")) })
						if err != nil || !strings.Contains(out, "КОНФЛИКТ") || !strings.Contains(out, strings.ToLower(s)) || !strings.Contains(out, strings.ToLower(l)) {
							t.Errorf("dry-run must expose static owner conflict: %s %v", out, err)
						}
						if attempt == 0 {
							previous = out
						} else if out != previous {
							t.Errorf("repeated dry-run differs: %s -> %s", previous, out)
						}
						if err := executeLongIdentCommand(t, args); err == nil || !strings.Contains(err.Error(), "КОНФЛИКТ") {
							t.Errorf("migration must refuse before any DDL: %v", err)
						}
						if after := longIdentSchemaSnapshot(t, db); !reflect.DeepEqual(before, after) {
							t.Errorf("dry-run/migrate changed schema, indexes, FKs or field map")
						}
						row, err := db.GetByID(ctx, independent.Name, id, independent)
						if err != nil || row["Значение"] != "keep" {
							t.Fatalf("independent data changed: %v %v", row, err)
						}
					}
				})
			})
		}
	}
}

func longIdentSchemaSnapshot(t *testing.T, db *storage.DB) []string {
	t.Helper()
	queries := []string{"SELECT type || ':' || name || ':' || tbl_name || ':' || coalesce(sql,'') FROM sqlite_master ORDER BY type,name"}
	if !db.IsSQLite() {
		queries = []string{
			"SELECT table_name || ':' || column_name || ':' || data_type || ':' || is_nullable || ':' || coalesce(column_default,'') FROM information_schema.columns WHERE table_schema=current_schema() ORDER BY table_name,ordinal_position",
			"SELECT tablename || ':' || indexname || ':' || indexdef FROM pg_indexes WHERE schemaname=current_schema() ORDER BY tablename,indexname",
			"SELECT c.relname || ':' || con.conname || ':' || pg_get_constraintdef(con.oid) FROM pg_constraint con JOIN pg_class c ON c.oid=con.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() ORDER BY c.relname,con.conname",
		}
	}
	queries = append(queries, "SELECT table_name || ':' || field_id || ':' || column_name || ':' || field_type FROM _schema_fields ORDER BY table_name,field_id")
	var out []string
	for _, q := range queries {
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
