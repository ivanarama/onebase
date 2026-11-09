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

// A current/legacy intersection is a configuration conflict even before either
// owner exists. It must never turn a successful fresh migration into a broken
// second launch, or perform DDL before discovering a column conflict.
func TestCurrentLegacyPreflightBeforeDDL_Matrix(t *testing.T) {
	for _, kind := range []string{"table", "column"} {
		for _, state := range []string{"empty", "short-owner", "both-modern"} {
			t.Run(kind+"/"+state, func(t *testing.T) {
				dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
					ctx := context.Background()
					s, l := strings.Repeat("Я", 31), strings.Repeat("Я", 31)+"Продолжение"
					short := &metadata.Entity{Name: s, Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
					long := &metadata.Entity{Name: l, Kind: metadata.KindCatalog, Fields: short.Fields}
					entities := []*metadata.Entity{short, long}
					if kind == "column" {
						short.Name = "Владелец"
						short.Fields = []metadata.Field{{Name: s, Type: metadata.FieldTypeString}}
						entities = []*metadata.Entity{{Name: short.Name, Kind: short.Kind, Fields: append(append([]metadata.Field{}, short.Fields...), metadata.Field{Name: l, Type: metadata.FieldTypeString})}}
					}
					if err := metadata.Validate(entities, nil); err != nil {
						t.Fatal(err)
					}
					// An independent pending change must not sneak in ahead of
					// the conflicting names, even when those tables do not exist.
					independent := &metadata.Entity{Name: "Независимый", Kind: metadata.KindCatalog, Fields: []metadata.Field{{ID: "keep", Name: "Значение", Type: metadata.FieldTypeString}}}
					if err := db.Migrate(ctx, []*metadata.Entity{independent}); err != nil {
						t.Fatal(err)
					}
					id := uuid.New()
					if err := db.Upsert(ctx, independent.Name, id, map[string]any{"Значение": "keep"}, independent); err != nil {
						t.Fatal(err)
					}
					if _, err := db.Exec(ctx, "CREATE INDEX keep_index ON независимый (значение)"); err != nil {
						t.Fatal(err)
					}
					if _, err := db.Exec(ctx, "CREATE TABLE keep_child (owner "+db.Dialect().TypeUUID()+" REFERENCES независимый(id))"); err != nil {
						t.Fatal(err)
					}
					if _, err := db.Exec(ctx, "INSERT INTO keep_child VALUES ("+db.Dialect().Placeholder(1)+")", id.String()); err != nil {
						t.Fatal(err)
					}
					// Seed the long owner first: migrating it alone after the
					// short table existed would treat that table as its legacy.
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
							col := metadata.ColumnName(entities[0].Fields[1])
							if _, err := db.Exec(ctx, "ALTER TABLE владелец ADD COLUMN "+col+" TEXT"); err != nil {
								t.Fatal(err)
							}
							if _, err := db.Exec(ctx, "UPDATE владелец SET "+col+" = 'long data'"); err != nil {
								t.Fatal(err)
							}
						}
					}
					before := currentLegacySchemaSnapshot(t, db)
					pending := *independent
					pending.Fields = append(append([]metadata.Field{}, pending.Fields...), metadata.Field{ID: "pending", Name: "НовоеПоле", Type: metadata.FieldTypeString})
					entities = append([]*metadata.Entity{&pending}, entities...)
					var first []storage.LongIdentAction
					for attempt := 0; attempt < 2; attempt++ {
						plan, err := db.PlanLongIdents(ctx, entities, nil, nil)
						if err != nil {
							t.Fatal(err)
						}
						if len(plan) != 1 || plan[0].Conflict == "" || plan[0].Old != strings.ToLower(s) || !strings.Contains(plan[0].Conflict, strings.ToLower(l)) {
							t.Errorf("want one static conflict naming both owners, got %v", plan)
						}
						if attempt == 0 {
							first = plan
						} else if !reflect.DeepEqual(first, plan) {
							t.Errorf("second plan changed: %v -> %v", first, plan)
						}
						for name, check := range map[string]func() error{
							"CheckLongIdents": func() error { return db.CheckLongIdents(ctx, entities, nil, nil, nil) },
							"Migrate":         func() error { return db.Migrate(ctx, entities) },
						} {
							if err := check(); err == nil || !strings.Contains(err.Error(), "КОНФЛИКТ") || !strings.Contains(err.Error(), strings.ToLower(l)) {
								t.Errorf("%s must refuse the configuration before DDL: %v", name, err)
							}
						}
						if after := currentLegacySchemaSnapshot(t, db); !reflect.DeepEqual(before, after) {
							t.Error("schema/indexes/FKs/field map changed")
						}
						row, err := db.GetByID(ctx, independent.Name, id, independent)
						if err != nil || row["Значение"] != "keep" {
							t.Fatalf("independent data changed: %v %v", row, err)
						}
						if state != "empty" {
							row, err = db.GetByID(ctx, short.Name, id, short)
							if err != nil || row[short.Fields[0].Name] != "short data" {
								t.Fatalf("short owner changed: %v %v", row, err)
							}
						}
						if state == "both-modern" {
							owner, field := long, "Наименование"
							if kind == "column" {
								owner, field = entities[1], l
							}
							row, err = db.GetByID(ctx, owner.Name, id, owner)
							if err != nil || row[field] != "long data" {
								t.Fatalf("long owner changed: %v %v", row, err)
							}
						}
						var child string
						if err := db.QueryRow(ctx, "SELECT owner FROM keep_child").Scan(&child); err != nil || child != id.String() {
							t.Fatalf("child changed: %s %v", child, err)
						}
					}
				})
			})
		}
	}
}

func currentLegacySchemaSnapshot(t *testing.T, db *storage.DB) []string {
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

// Sharing only the old prefix is safe on a fresh schema. The new preflight
// must not ban the original use case for hashing long identifiers.
func TestSharedLegacyPrefixFreshOwnersRemainIndependent_Matrix(t *testing.T) {
	for _, kind := range []string{"table", "column"} {
		t.Run(kind, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				ctx := context.Background()
				a, b := strings.Repeat("Я", 31)+"Первый", strings.Repeat("Я", 31)+"Второй"
				entities := []*metadata.Entity{
					{Name: a, Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Значение", Type: metadata.FieldTypeString}}},
					{Name: b, Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Значение", Type: metadata.FieldTypeString}}},
				}
				if kind == "column" {
					entities = []*metadata.Entity{{Name: "Владелец", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: a, Type: metadata.FieldTypeString}, {Name: b, Type: metadata.FieldTypeString}}}}
				}
				if err := metadata.Validate(entities, nil); err != nil {
					t.Fatal(err)
				}
				id := uuid.New()
				for attempt := 0; attempt < 2; attempt++ {
					if plan, err := db.PlanLongIdents(ctx, entities, nil, nil); err != nil || len(plan) != 0 {
						t.Fatalf("fresh shared prefix must be allowed: %v %v", plan, err)
					}
					if err := db.Migrate(ctx, entities); err != nil {
						t.Fatal(err)
					}
					for i, e := range entities {
						values := map[string]any{}
						for j, f := range e.Fields {
							values[f.Name] = fmt.Sprintf("owner-%d-field-%d", i, j)
						}
						if attempt == 0 {
							if err := db.Upsert(ctx, e.Name, id, values, e); err != nil {
								t.Fatal(err)
							}
						}
						row, err := db.GetByID(ctx, e.Name, id, e)
						if err != nil {
							t.Fatal(err)
						}
						for field, want := range values {
							if row[field] != want {
								t.Errorf("owners mixed: %v want %v", row, values)
							}
						}
					}
				}
			})
		})
	}
}

// The full-project public preflight must inspect every supported namespace,
// including tables/columns that will only be created later in the migration.
func TestCurrentLegacyPreflightAllNamespaces_Matrix(t *testing.T) {
	long := strings.Repeat("Я", 31) + "Продолжение"
	fields := []metadata.Field{{Name: strings.Repeat("Я", 31), Type: metadata.FieldTypeNumber}, {Name: long, Type: metadata.FieldTypeNumber}}
	for _, scope := range []string{"entity", "table-part", "register", "register-totals", "info-register", "account-register", "account-totals"} {
		for _, kind := range []string{"table", "column"} {
			t.Run(scope+"/"+kind, func(t *testing.T) {
				dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
					ctx := context.Background()
					var es []*metadata.Entity
					var rs []*metadata.Register
					var irs []*metadata.InfoRegister
					var ars []*metadata.AccountRegister
					name := "Владелец"
					if kind == "table" {
						name = long
					}
					var logical string
					var migrate func() error
					switch scope {
					case "entity":
						es = []*metadata.Entity{{Name: name, Kind: metadata.KindCatalog, Fields: fields}}
						logical = metadata.TableLogical(name)
						migrate = func() error { return db.Migrate(ctx, es) }
					case "table-part":
						es = []*metadata.Entity{{Name: "Документ", Kind: metadata.KindDocument, TableParts: []metadata.TablePart{{Name: name, Fields: fields}}}}
						logical = metadata.TablePartTableLogical(es[0].Name, name)
						migrate = func() error { return db.Migrate(ctx, es) }
					case "register", "register-totals":
						rs = []*metadata.Register{{Name: name, Resources: fields, Totals: metadata.RegisterTotals{Enabled: scope == "register-totals"}}}
						logical = metadata.RegisterTableLogical(name)
						if scope == "register-totals" {
							logical = metadata.RegisterTotalsTableLogical(name)
						}
						migrate = func() error { return db.MigrateRegisters(ctx, rs) }
					case "info-register":
						irs = []*metadata.InfoRegister{{Name: name, Dimensions: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}}, Resources: fields}}
						logical = metadata.InfoRegTableLogical(name)
						migrate = func() error { return db.MigrateInfoRegisters(ctx, irs) }
					case "account-register", "account-totals":
						ars = []*metadata.AccountRegister{{Name: name, Accounts: "Счета", Resources: fields, Totals: metadata.RegisterTotals{Enabled: scope == "account-totals"}}}
						logical = metadata.AccountRegTableLogical(name)
						if scope == "account-totals" {
							logical = metadata.AccountRegTotalsTableLogical(name)
							// Only turnover columns intersect: neither resource's
							// own current name equals the other's old prefix.
							a := strings.Repeat("x", 58)
							ars[0].Resources = []metadata.Field{{Name: a, Type: metadata.FieldTypeNumber}, {Name: a + "_дтПродолжение", Type: metadata.FieldTypeNumber}}
						}
						migrate = func() error { return db.MigrateAccountRegisters(ctx, ars) }
					}
					if kind == "table" {
						old := metadata.LegacySQLIdents(logical)[1]
						es = append(es, &metadata.Entity{Name: old, Kind: metadata.KindCatalog})
					}
					if err := metadata.Validate(es, nil); err != nil {
						t.Fatal(err)
					}
					if err := db.EnsureSchemaMapSchema(ctx); err != nil {
						t.Fatal(err)
					}
					before := currentLegacySchemaSnapshot(t, db)
					plan, err := db.PlanLongIdents(ctx, es, rs, irs, ars)
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, a := range plan {
						if a.Conflict != "" && ((kind == "table" && a.Table == "") || (kind == "column" && a.Table == metadata.SQLIdent(logical))) {
							found = true
						}
					}
					if !found {
						t.Fatalf("missing conflict for %s %s: %v", scope, kind, plan)
					}
					if err := db.CheckLongIdents(ctx, es, rs, irs, ars); err == nil || !strings.Contains(err.Error(), "КОНФЛИКТ") {
						t.Errorf("public check must refuse: %v", err)
					}
					// Per-kind migration sees all column owners. Cross-kind
					// table ownership is checked by the full project/CLI gate.
					if kind == "column" || scope == "entity" || scope == "table-part" {
						if err := migrate(); err == nil || !strings.Contains(err.Error(), "КОНФЛИКТ") {
							t.Errorf("migration must refuse: %v", err)
						}
					}
					if after := currentLegacySchemaSnapshot(t, db); !reflect.DeepEqual(before, after) {
						t.Error("preflight changed schema")
					}
				})
			})
		}
	}
}
