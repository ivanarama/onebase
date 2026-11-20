package query_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestInfoRegQueryNeverProjectsEditRevisionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := &metadata.InfoRegister{Name: "RevisionQuery", Dimensions: []metadata.Field{{Name: "Key", Type: "string"}}, Resources: []metadata.Field{{Name: "Value", Type: "string"}}}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		if err := db.InfoRegSet(ctx, ir, map[string]any{"Key": "X"}, map[string]any{"Value": "visible"}, nil); err != nil {
			t.Fatal(err)
		}
		for _, filter := range []*storage.Predicate{nil, {Field: "Value", Op: "eq", Value: "visible"}} {
			opts := query.CompileOpts{InfoRegs: []*metadata.InfoRegister{ir}, Dialect: db.Dialect()}
			if filter != nil {
				opts.RowFilters = map[query.SourceRef]*storage.Predicate{{Kind: "inforeg", Name: ir.Name}: filter}
			}
			for _, src := range []string{
				`ВЫБРАТЬ * ИЗ РегистрСведений.RevisionQuery`,
				`ВЫБРАТЬ Р.* ИЗ РегистрСведений.RevisionQuery КАК Р`,
				`ВЫБРАТЬ * ИЗ (ВЫБРАТЬ Р.* ИЗ РегистрСведений.RevisionQuery КАК Р) КАК Т`,
				`ВЫБРАТЬ Р.* ИЗ РегистрСведений.RevisionQuery КАК Р ЛЕВОЕ СОЕДИНЕНИЕ РегистрСведений.RevisionQuery КАК Д ПО Р.Key = Д.Key`,
				`ВЫБРАТЬ * ИЗ РегистрСведений.RevisionQuery.СрезПоследних()`,
			} {
				compiled, err := query.Compile(src, opts)
				if err != nil {
					t.Fatalf("%s: %v", src, err)
				}
				rows, err := db.Query(ctx, compiled.SQL, compiled.Args...)
				if err != nil {
					t.Fatalf("%s: %v\n%s", src, err, compiled.SQL)
				}
				for _, col := range rows.FieldNames() {
					if strings.EqualFold(col, storage.InfoRegEditRevisionColumn) {
						t.Fatal("revision leaked through wildcard")
					}
				}
				count := 0
				for rows.Next() {
					count++
				}
				err = rows.Err()
				rows.Close()
				if err != nil || count != 1 {
					t.Fatalf("rows %d %v", count, err)
				}
			}
		}
	})
}
