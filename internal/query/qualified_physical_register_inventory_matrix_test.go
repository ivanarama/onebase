package query_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestQualifiedPhysicalRegisterSystemColumnsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		catalog := &metadata.Entity{Name: "RegisterInventoryOuter2542", Kind: metadata.KindCatalog}
		if err := db.Migrate(ctx, []*metadata.Entity{catalog}); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, catalog.Name, uuid.New(), map[string]any{}, catalog); err != nil {
			t.Fatal(err)
		}
		reg := &metadata.Register{Name: "RawAccum2542", Dimensions: []metadata.Field{{Name: "Key", Type: metadata.FieldTypeString}}, Resources: []metadata.Field{{Name: "Amount", Type: metadata.FieldTypeNumber}}}
		ir := &metadata.InfoRegister{Name: "RawInfo2542", Dimensions: []metadata.Field{{Name: "Key", Type: metadata.FieldTypeString}}, Resources: []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}}}
		ar := &metadata.AccountRegister{Name: "RawAccount2542", Accounts: "Chart2542", Resources: []metadata.Field{{Name: "Amount", Type: metadata.FieldTypeNumber}}}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{ar}); err != nil {
			t.Fatal(err)
		}
		if err := db.WriteMovements(ctx, reg.Name, "Source", uuid.New(), []map[string]any{{"Key": "one", "Amount": 1, "ВидДвижения": "Приход"}}, reg, nil); err != nil {
			t.Fatal(err)
		}
		if err := db.InfoRegSet(ctx, ir, map[string]any{"Key": "one"}, map[string]any{"Value": "one"}, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, "UPDATE "+metadata.InfoRegTableName(ir.Name)+" SET recorder_type = 'Source'"); err != nil {
			t.Fatal(err)
		}
		d := db.Dialect()
		insert := "INSERT INTO " + metadata.AccountRegTableName(ar.Name) + " (id, period, регистратор, регистратор_тип, счётдт, счёткт, amount) VALUES ("
		for i := 1; i <= 7; i++ {
			if i > 1 {
				insert += ", "
			}
			insert += d.Placeholder(i)
		}
		if _, err := db.Exec(ctx, insert+")", uuid.New().String(), time.Now().UTC(), uuid.New().String(), "Source", "10", "20", 1); err != nil {
			t.Fatal(err)
		}
		opts := query.CompileOpts{Entities: []*metadata.Entity{catalog}, Registers: []*metadata.Register{reg}, InfoRegs: []*metadata.InfoRegister{ir}, AccountRegs: []*metadata.AccountRegister{ar}, Dialect: d}
		for _, tc := range []struct{ kind, name, column string }{
			{"РегистрНакопления", reg.Name, "recorder_type"},
			{"РегистрСведений", ir.Name, "recorder_type"},
			{"РегистрБухгалтерии", ar.Name, "регистратор_тип"},
		} {
			for _, star := range []bool{false, true} {
				t.Run(fmt.Sprintf("present/%s/star=%t", tc.kind, star), func(t *testing.T) {
					local := tc.kind + "." + tc.name + " КАК М"
					if star {
						local = "(ВЫБРАТЬ З.* ИЗ " + tc.kind + "." + tc.name + " КАК З) КАК М"
					}
					text := fmt.Sprintf(`ВЫБРАТЬ М.Х КАК Х ИЗ (ВЫБРАТЬ "ok" КАК Х, "outer" КАК %s ИЗ Справочник.%s) КАК М
						ГДЕ "Source" В (ВЫБРАТЬ М.%s ИЗ %s)`, tc.column, catalog.Name, tc.column, local)
					compiled, err := query.Compile(text, opts)
					if err != nil {
						t.Fatal(err)
					}
					rows, _, err := query.Run(ctx, db, &compiled)
					if err != nil || len(rows) != 1 || rows[0]["х"] != "ok" {
						t.Fatalf("rows=%v err=%v; want real local Source, not outer\nSQL: %s", rows, err, compiled.SQL)
					}
				})
			}
		}
		for _, column := range []string{"period", storage.InfoRegEditRevisionColumn, "UnknownScalar2542"} {
			for _, star := range []bool{false, true} {
				t.Run(fmt.Sprintf("absent/nonperiodic_info_%s/star=%t", column, star), func(t *testing.T) {
					local := "РегистрСведений." + ir.Name + " КАК М"
					if star {
						local = "(ВЫБРАТЬ З.* ИЗ РегистрСведений." + ir.Name + " КАК З) КАК М"
					}
					text := fmt.Sprintf(`ВЫБРАТЬ М.Х КАК Х ИЗ (ВЫБРАТЬ "ok" КАК Х, "outer" КАК %s ИЗ Справочник.%s) КАК М
						ГДЕ "outer" В (ВЫБРАТЬ М.%s ИЗ %s)`, column, catalog.Name, column, local)
					compiled, err := query.Compile(text, opts)
					if err != nil {
						return
					}
					if rows, _, err := query.Run(ctx, db, &compiled); err == nil {
						t.Fatalf("non-exported/missing info field borrowed outer data: %v\n%s", rows, compiled.SQL)
					}
				})
			}
		}
	})
}
