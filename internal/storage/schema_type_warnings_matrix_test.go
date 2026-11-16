package storage_test

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestUntrackedFieldTypeWarningsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		field := func(name string) metadata.Field { return metadata.Field{Name: name, Type: metadata.FieldTypeString} }
		target := &metadata.Entity{Name: "Цель", Kind: metadata.KindCatalog}
		e := &metadata.Entity{Name: "Источник", Kind: metadata.KindCatalog,
			Fields:     []metadata.Field{field("Дата"), field("Мастер"), field("Флаг"), field("Текст"), {Name: "Число", Type: metadata.FieldTypeNumber, Length: 15, Scale: 2}, {Name: "Учтено", ID: "tracked", Type: metadata.FieldTypeString}},
			TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{field("Флаг")}}}}
		reg := &metadata.Register{Name: "Обороты", Dimensions: []metadata.Field{field("Измерение")}, Resources: []metadata.Field{field("Ресурс")}, Attributes: []metadata.Field{field("Атрибут")}}
		ir := &metadata.InfoRegister{Name: "Сведения", Dimensions: []metadata.Field{field("Измерение")}, Resources: []metadata.Field{field("Ресурс")}}
		entities := []*metadata.Entity{target, e}
		regs := []*metadata.Register{reg}
		irs := []*metadata.InfoRegister{ir}
		migrate := func() {
			t.Helper()
			for _, err := range []error{db.Migrate(ctx, entities), db.MigrateRegisters(ctx, regs), db.MigrateInfoRegisters(ctx, irs)} {
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		warnings := func() []storage.SchemaTypeWarning {
			t.Helper()
			ws, err := db.UntrackedFieldTypeWarnings(ctx, entities, regs, irs)
			if err != nil {
				t.Fatal(err)
			}
			return ws
		}
		// Missing tables/columns must not be reported as incompatible types.
		if ws := warnings(); len(ws) != 0 {
			t.Fatalf("fresh database: %+v", ws)
		}
		migrate()
		if !db.IsSQLite() {
			if _, err := db.Exec(ctx, `ALTER TABLE источник ALTER COLUMN текст TYPE VARCHAR(80)`); err != nil {
				t.Fatal(err)
			}
		}
		if ws := warnings(); len(ws) != 0 {
			t.Fatalf("matching types and aliases: %+v", ws)
		}
		if _, err := db.Exec(ctx, `INSERT INTO источник (id, дата, мастер, флаг) VALUES ('11111111-1111-1111-1111-111111111111', '2026-10-21', '22222222-2222-2222-2222-222222222222', 'true')`); err != nil {
			t.Fatal(err)
		}
		// A tracked field is handled by the existing restructuring plan, not these warnings.
		e.Fields[5].Type = metadata.FieldTypeBool
		e.Fields[0].Type = metadata.FieldTypeDate
		e.Fields[1].Type = "reference"
		e.Fields[1].RefEntity = target.Name
		e.Fields[2].Type = metadata.FieldTypeBool
		e.Fields = append(e.Fields, metadata.Field{Name: "Новый", Type: metadata.FieldTypeBool})
		e.TableParts[0].Fields[0].Type = metadata.FieldTypeBool
		reg.Dimensions[0].Type = metadata.FieldTypeBool
		reg.Resources[0].Type = metadata.FieldTypeBool
		reg.Attributes[0].Type = metadata.FieldTypeBool
		ir.Dimensions[0].Type = metadata.FieldTypeBool
		ir.Resources[0].Type = metadata.FieldTypeBool
		expected := 7
		if !db.IsSQLite() {
			expected += 2
		}
		before := physicalSchemaSnapshot(t, db)
		ws := warnings()
		if len(ws) != expected {
			t.Fatalf("warnings=%+v; want %d", ws, expected)
		}
		for _, w := range ws {
			if w.Field == "Учтено" || w.Field == "Новый" || w.Field == "Текст" || w.Field == "Число" {
				t.Fatalf("unexpected warning: %+v", w)
			}
			if w.ActualType == "" || w.ExpectedType == "" || !strings.Contains(w.String(), "добавление id") {
				t.Fatalf("incomplete warning: %+v", w)
			}
		}
		if again := warnings(); !reflect.DeepEqual(again, ws) {
			t.Fatalf("nonrepeatable diagnostic: %+v / %+v", ws, again)
		}
		if after := physicalSchemaSnapshot(t, db); before != after {
			t.Fatalf("read-only diagnostic changed schema:\n%s\n%s", before, after)
		}
		var delivered []storage.SchemaTypeWarning
		db.SetSchemaOptions(storage.SchemaOptions{AllowDestructive: true, Warn: func(w storage.SchemaTypeWarning) { delivered = append(delivered, w) }})
		migrate()
		if len(delivered) != expected {
			t.Fatalf("ordinary migration delivered %+v; want %d warnings", delivered, expected)
		}
		// Warnings are not changes, including with --allow-destructive. Physical
		// types and values are preserved; PG's existing legacy column rename remains.
		refCol := "мастер"
		if !db.IsSQLite() {
			refCol = "мастер_id"
		}
		var date, ref, flag string
		if err := db.QueryRow(ctx, fmt.Sprintf(`SELECT CAST(дата AS TEXT), CAST(%s AS TEXT), CAST(флаг AS TEXT) FROM источник`, refCol)).Scan(&date, &ref, &flag); err != nil {
			t.Fatal(err)
		}
		if date != "2026-10-21" || ref != "22222222-2222-2222-2222-222222222222" || flag != "true" {
			t.Fatalf("untracked values changed: %q %q %q", date, ref, flag)
		}
		afterWarnings := warnings()
		if len(afterWarnings) != expected {
			t.Fatalf("mismatch disappeared after migrate: %+v", afterWarnings)
		}
		for _, w := range afterWarnings {
			if !strings.EqualFold(w.ActualType, "text") {
				t.Fatalf("untracked physical type changed: %+v", w)
			}
		}
		// Unlike entities/accumulation registers, table parts and info registers
		// do not run the legacy PG rename. Their absent _id columns are new work.
		e.TableParts[0].Fields[0].Type = "reference"
		e.TableParts[0].Fields[0].RefEntity = target.Name
		ir.Resources[0].Type = "reference"
		ir.Resources[0].RefEntity = target.Name
		if got := len(warnings()); got != expected-2 {
			t.Fatalf("inferred unsupported legacy rename: warnings=%d want %d", got, expected-2)
		}
		e.TableParts[0].Fields[0].Type = metadata.FieldTypeBool
		e.TableParts[0].Fields[0].RefEntity = ""
		ir.Resources[0].Type = metadata.FieldTypeBool
		ir.Resources[0].RefEntity = ""
		// An unrelated renamed field is a new additive column, not guessed identity.
		e.Fields[2].Name = "ДругойФлаг"
		if got := len(warnings()); got != expected-1 {
			t.Fatalf("guessed a rename: warnings=%d want %d", got, expected-1)
		}
	})
}

func physicalSchemaSnapshot(t *testing.T, db *storage.DB) string {
	t.Helper()
	query := `SELECT name, sql FROM sqlite_master ORDER BY name`
	if !db.IsSQLite() {
		query = `SELECT table_name || '.' || column_name, data_type FROM information_schema.columns WHERE table_schema=current_schema() ORDER BY table_name, column_name`
	}
	rows, err := db.Query(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		var definition *string
		if err := rows.Scan(&name, &definition); err != nil {
			t.Fatal(err)
		}
		if definition != nil {
			out = append(out, name+"="+*definition)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

func TestUntrackedFieldTypeAliasesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ddl := `CREATE TABLE алиасы (текст VARCHAR(60), логика BOOLEAN, целаялогика BIGINT, число TEXT)`
		if !db.IsSQLite() {
			ddl = `CREATE TABLE алиасы (текст VARCHAR(60), логика BOOL, целаялогика BOOLEAN, число DECIMAL(12,2))`
		}
		if _, err := db.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
		e := &metadata.Entity{Name: "Алиасы", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Текст", Type: metadata.FieldTypeString}, {Name: "Логика", Type: metadata.FieldTypeBool}, {Name: "ЦелаяЛогика", Type: metadata.FieldTypeBool}, {Name: "Число", Type: metadata.FieldTypeNumber}}}
		ws, err := db.UntrackedFieldTypeWarnings(ctx, []*metadata.Entity{e}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(ws) != 0 {
			t.Fatalf("compatible physical aliases reported: %+v", ws)
		}
	})
}

// A warning is not a preservation guarantee for the separate SQLite PK rebuild.
// Exercise the public migration with a reordered key and a resource type change.
func TestUntrackedFieldTypeWarningWithInfoRegPKChangeMatrix(t *testing.T) {
	for _, destructive := range []bool{false, true} {
		t.Run(fmt.Sprintf("allow-destructive=%t", destructive), func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				ctx := context.Background()
				ir := &metadata.InfoRegister{Name: "СменаКлюча",
					Dimensions: []metadata.Field{{Name: "Первое", Type: metadata.FieldTypeString}, {Name: "Второе", Type: metadata.FieldTypeString}},
					Resources:  []metadata.Field{{Name: "Флаг", Type: metadata.FieldTypeString}}}
				if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(ctx, `INSERT INTO инфо_сменаключа (первое, второе, флаг) VALUES ('a', 'b', '001')`); err != nil {
					t.Fatal(err)
				}
				ir.Dimensions[0], ir.Dimensions[1] = ir.Dimensions[1], ir.Dimensions[0]
				ir.Resources[0].Type = metadata.FieldTypeBool
				var warnings []storage.SchemaTypeWarning
				db.SetSchemaOptions(storage.SchemaOptions{AllowDestructive: destructive,
					Warn: func(w storage.SchemaTypeWarning) { warnings = append(warnings, w) }})
				if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
					t.Fatal(err)
				}
				if len(warnings) != 1 || warnings[0].Column != "флаг" {
					t.Fatalf("migration warnings: %+v", warnings)
				}
				text := warnings[0].String()
				for _, want := range []string{"план реструктуризации", "Другие операции миграции могут изменить тип и данные", "при смене ключа", "даже без --allow-destructive"} {
					if !strings.Contains(text, want) {
						t.Fatalf("missing warning qualifier %q: %s", want, text)
					}
				}
				var value, typ string
				if err := db.QueryRow(ctx, `SELECT CAST(флаг AS TEXT) FROM инфо_сменаключа WHERE первое='a' AND второе='b'`).Scan(&value); err != nil {
					t.Fatal(err)
				}
				query := `SELECT type FROM pragma_table_info('инфо_сменаключа') WHERE name='флаг'`
				wantType, wantValue := "INTEGER", "1"
				if !db.IsSQLite() {
					query = `SELECT data_type FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='инфо_сменаключа' AND column_name='флаг'`
					wantType, wantValue = "text", "001"
				}
				if err := db.QueryRow(ctx, query).Scan(&typ); err != nil {
					t.Fatal(err)
				}
				if typ != wantType || value != wantValue {
					t.Fatalf("PK migration type/value: %q/%q; want %q/%q", typ, value, wantType, wantValue)
				}
			})
		})
	}
}
