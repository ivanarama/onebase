package query_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Compare the public compiler's source descriptor with the driver's actual
// columns. This covers alternate generators (totals, date bounds, periodicity,
// references, attributes and dialect-specific slices) rather than restating a
// list of expected suffixes alongside the implementation.
func TestVirtualSourceOutputColumnsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		target := &metadata.Entity{Name: "КонтрагентВТ", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: "string"}}}
		if err := db.Migrate(ctx, []*metadata.Entity{target}); err != nil {
			t.Fatal(err)
		}
		if err := db.EnsureAccountsTable(ctx); err != nil {
			t.Fatal(err)
		}
		check := func(t *testing.T, source string, opts query.CompileOpts) {
			t.Helper()
			opts.Dialect = db.Dialect()
			opts.Entities = []*metadata.Entity{target}
			opts.Params = map[string]any{"Начало": time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), "Конец": time.Date(2026, 2, 3, 0, 0, 0, 0, time.UTC)}
			res, err := query.Compile("ВЫБРАТЬ П.* ИЗ "+source+" КАК П", opts)
			if err != nil {
				t.Fatal(err)
			}
			var sourceColumns []string
			for _, src := range res.SourceOccurrences {
				if src.Qualifier == "п" && src.Virtual {
					sourceColumns = src.OutputColumns
				}
			}
			if len(sourceColumns) == 0 {
				t.Fatalf("unproved source: %s", res.SQL)
			}
			_, columns, err := db.RunQuery(ctx, res.SQL, res.Args)
			if err != nil {
				t.Fatalf("%v\n%s", err, res.SQL)
			}
			if !reflect.DeepEqual(columns, sourceColumns) {
				t.Fatalf("driver columns %v != source descriptor %v\n%s", columns, sourceColumns, res.SQL)
			}
		}
		for _, totals := range []bool{false, true} {
			reg := &metadata.Register{Name: fmt.Sprintf("СкладСхема%v", totals), Dimensions: []metadata.Field{{Name: "Контрагент", Type: "ref", RefEntity: target.Name}}, Resources: []metadata.Field{{Name: "Количество", Type: "number"}}, Totals: metadata.RegisterTotals{Enabled: totals}}
			if !totals {
				reg.Attributes = []metadata.Field{{Name: "Примечание", Type: "string"}}
			}
			if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
				t.Fatal(err)
			}
			for _, vt := range []string{"Остатки()", "Остатки(&Конец)", "Обороты()", "Обороты(&Начало,&Конец,Месяц)", "ОстаткиИОбороты()", "ОстаткиИОбороты(&Начало,&Конец)"} {
				t.Run(reg.Name+"/"+vt, func(t *testing.T) {
					check(t, "РегистрНакопления."+reg.Name+"."+vt, query.CompileOpts{Registers: []*metadata.Register{reg}})
				})
			}
			ar := &metadata.AccountRegister{Name: fmt.Sprintf("БухСхема%v", totals), Accounts: "Основной", Resources: []metadata.Field{{Name: "Сумма", Type: "number"}}, Subconto: []metadata.Field{{Name: "Аналитика", Type: "string"}}, Totals: metadata.RegisterTotals{Enabled: totals}}
			if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{ar}); err != nil {
				t.Fatal(err)
			}
			for _, vt := range []string{"Остатки()", "Остатки(&Конец)", "Обороты()", "Обороты(&Начало,&Конец)"} {
				t.Run(ar.Name+"/"+vt, func(t *testing.T) {
					check(t, "РегистрБухгалтерии."+ar.Name+"."+vt, query.CompileOpts{AccountRegs: []*metadata.AccountRegister{ar}})
				})
			}
		}
		for _, periodic := range []bool{false, true} {
			ir := &metadata.InfoRegister{Name: fmt.Sprintf("СведенияСхема%v", periodic), Periodic: periodic, Dimensions: []metadata.Field{{Name: "Контрагент", Type: "ref", RefEntity: target.Name}}, Resources: []metadata.Field{{Name: "Связь", Type: "ref", RefEntity: target.Name}}}
			if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
				t.Fatal(err)
			}
			for _, vt := range []string{"СрезПоследних()", "СрезПервых()"} {
				t.Run(ir.Name+"/"+vt, func(t *testing.T) {
					check(t, "РегистрСведений."+ir.Name+"."+vt, query.CompileOpts{InfoRegs: []*metadata.InfoRegister{ir}})
				})
			}
		}
	})
}
