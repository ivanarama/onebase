package query_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestQualifiedPhysicalRawSystemColumnsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		catalog := &metadata.Entity{Name: "RawInventoryCatalog2542", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
		document := &metadata.Entity{Name: "RawInventoryDocument2542", Kind: metadata.KindDocument,
			Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}}}
		entities := []*metadata.Entity{catalog, document}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		for _, entity := range entities {
			if err := db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{}, entity); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec(ctx, "UPDATE "+metadata.TableName(document.Name)+" SET _version = 42"); err != nil {
			t.Fatal(err)
		}
		opts := query.CompileOpts{Entities: entities, Dialect: db.Dialect()}
		for _, tc := range []struct{ column, outer, local string }{
			{"posted", "ИСТИНА", "ЛОЖЬ"},
			{"deletion_mark", "ИСТИНА", "ЛОЖЬ"},
			{"_version", "17", "42"},
		} {
			for _, star := range []bool{false, true} {
				t.Run(fmt.Sprintf("present/%s/star=%t", tc.column, star), func(t *testing.T) {
					local := "Документ." + document.Name + " КАК М"
					if star {
						local = "(ВЫБРАТЬ З.* ИЗ Документ." + document.Name + " КАК З) КАК М"
					}
					// The real local value differs from the namesake outer output,
					// so accidental correlation cannot pass this check.
					text := fmt.Sprintf(`ВЫБРАТЬ М.Х КАК Х
						ИЗ (ВЫБРАТЬ "ok" КАК Х, %s КАК %s ИЗ Справочник.%s) КАК М
						ГДЕ %s В (ВЫБРАТЬ М.%s ИЗ %s)`, tc.outer, tc.column, catalog.Name, tc.local, tc.column, local)
					compiled, err := query.Compile(text, opts)
					if err != nil {
						t.Fatalf("Compile: %v", err)
					}
					rows, columns, err := query.Run(ctx, db, &compiled)
					if err != nil {
						t.Fatalf("Run: %v\nSQL: %s", err, compiled.SQL)
					}
					if len(columns) != 1 || columns[0] != "х" || len(rows) != 1 || rows[0]["х"] != "ok" {
						t.Fatalf("columns=%v rows=%v; want one local-value match with х=ok", columns, rows)
					}
				})
			}
		}
		for _, column := range []string{"posted", "UnknownScalar2542"} {
			for _, star := range []bool{false, true} {
				t.Run(fmt.Sprintf("absent/catalog_%s/star=%t", column, star), func(t *testing.T) {
					local := "Справочник." + catalog.Name + " КАК М"
					if star {
						local = "(ВЫБРАТЬ З.* ИЗ Справочник." + catalog.Name + " КАК З) КАК М"
					}
					text := fmt.Sprintf(`ВЫБРАТЬ М.Х КАК Х
						ИЗ (ВЫБРАТЬ "ok" КАК Х, ИСТИНА КАК %s ИЗ Справочник.%s) КАК М
						ГДЕ ИСТИНА В (ВЫБРАТЬ М.%s ИЗ %s)`, column, catalog.Name, column, local)
					compiled, err := query.Compile(text, opts)
					if err != nil {
						return
					}
					if rows, _, err := query.Run(ctx, db, &compiled); err == nil {
						t.Fatalf("absent local column borrowed outer TRUE: rows=%v\nSQL: %s", rows, compiled.SQL)
					}
				})
			}
		}
	})
}
