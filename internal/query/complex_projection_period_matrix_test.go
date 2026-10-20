package query_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// The diagnostic must describe the existing public execution, without changing
// SQL values or enabling runtime conversion for a complex projection.
func TestComplexProjectionRegisterPeriodMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		reg := &metadata.Register{Name: "ДвиженияДат", Resources: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}}}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatal(err)
		}
		at := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
		if err := db.WriteMovements(ctx, reg.Name, "Документ", uuid.New(), []map[string]any{{"ВидДвижения": "Приход", "Количество": 1}}, reg, &at); err != nil {
			t.Fatal(err)
		}
		ir := &metadata.InfoRegister{Name: "ДатыСведений", Periodic: true, Dimensions: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}}, Resources: []metadata.Field{{Name: "Метка", Type: metadata.FieldTypeString}}}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		if err := db.InfoRegSet(ctx, ir, map[string]any{"Ключ": "a"}, map[string]any{"Метка": "a"}, &at); err != nil {
			t.Fatal(err)
		}
		opts := query.CompileOpts{Registers: []*metadata.Register{reg}, InfoRegs: []*metadata.InfoRegister{ir}, Dialect: db.Dialect(), Params: map[string]any{"Граница": at.Add(time.Hour)}}
		for _, tc := range []struct {
			name, text, key string
			count           int
			simple          bool
		}{
			{"simple", `ВЫБРАТЬ Период ИЗ РегистрНакопления.ДвиженияДат`, "period", 1, true},
			{"simple alias", `ВЫБРАТЬ Д.ПеРиОд КАК Момент ИЗ РегистрНакопления.ДвиженияДат КАК Д`, "момент", 1, true},
			{"union all", `ВЫБРАТЬ Период ИЗ РегистрНакопления.ДвиженияДат ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Период ИЗ РегистрНакопления.ДвиженияДат`, "period", 2, false},
			{"union", `ВЫБРАТЬ Период ИЗ РегистрНакопления.ДвиженияДат ОБЪЕДИНИТЬ ВЫБРАТЬ Период ИЗ РегистрНакопления.ДвиженияДат`, "period", 1, false},
			{"qualified mixed case alias", `ВЫБРАТЬ Д.ПеРиОд КАК Момент ИЗ РегистрНакопления.ДвиженияДат КАК Д ГДЕ Д.Период < &Граница ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Д.Период ИЗ РегистрНакопления.ДвиженияДат КАК Д`, "момент", 2, false},
			{"Latin period", `SELECT D.PeRiOd AS moment FROM AccumulationRegister.ДвиженияДат AS D UNION ALL SELECT period FROM AccumulationRegister.ДвиженияДат`, "moment", 2, false},
			{"derived alias", `ВЫБРАТЬ Т.Момент ИЗ (ВЫБРАТЬ Д.Период КАК Момент ИЗ РегистрНакопления.ДвиженияДат КАК Д) КАК Т`, "момент", 1, false},
			{"information register", `ВЫБРАТЬ Период ИЗ РегистрСведений.ДатыСведений ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ period ИЗ РегистрСведений.ДатыСведений`, "period", 2, false},
			{"information slice", `ВЫБРАТЬ С.Период ИЗ РегистрСведений.ДатыСведений.СрезПоследних() КАК С ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Период ИЗ РегистрСведений.ДатыСведений.СрезПервых()`, "period", 2, false},
			{"periodic turnovers", `ВЫБРАТЬ Период ИЗ РегистрНакопления.ДвиженияДат.Обороты(,,Запись) ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Период ИЗ РегистрНакопления.ДвиженияДат.Обороты(,,Запись)`, "period", 2, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				res, err := query.Compile(tc.text, opts)
				if err != nil {
					t.Fatal(err)
				}
				if tc.simple {
					if len(res.UnconvertedTypedFields) != 0 {
						t.Fatalf("simple warning: %v", res.UnconvertedTypedFields)
					}
				} else {
					if len(res.UnconvertedTypedFields) == 0 {
						t.Errorf("missing service-date warning: %s", res.SQL)
					}
					for _, field := range res.UnconvertedTypedFields {
						if !strings.EqualFold(field, "Период") && !strings.EqualFold(field, "period") {
							t.Errorf("unexpected field %q", field)
						}
					}
					if len(res.DateColumns) != 0 || len(res.TypedColumns) != 0 || len(res.RefColumns) != 0 {
						t.Errorf("complex runtime types changed: %+v", res)
					}
				}
				rows, cols, err := query.Run(ctx, db, &res)
				if err != nil {
					t.Fatalf("%v; SQL=%s", err, res.SQL)
				}
				if !reflect.DeepEqual(cols, []string{tc.key}) || len(rows) != tc.count {
					t.Fatalf("cols=%v rows=%v", cols, rows)
				}
				if tc.simple {
					date, ok := rows[0][tc.key].(time.Time)
					if !ok || !date.Equal(at) {
						t.Fatalf("simple date = %#v (%T), want %s", rows[0][tc.key], rows[0][tc.key], at)
					}
				} else {
					raw, rawCols, err := db.RunQuery(ctx, res.SQL, res.Args)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(rows, raw) || !reflect.DeepEqual(cols, rawCols) {
						t.Fatalf("complex values converted: got %v, raw %v", rows, raw)
					}
					if db.Dialect().Name() == "sqlite" {
						if _, ok := rows[0][tc.key].(string); !ok {
							t.Fatalf("SQLite raw period = %T, want string", rows[0][tc.key])
						}
					}
				}
			})
		}
	})
}

func TestComplexProjectionDoesNotInventRegisterPeriod(t *testing.T) {
	opts := query.CompileOpts{
		Entities:    []*metadata.Entity{{Name: "Строки", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Период", Type: metadata.FieldTypeString}}}, {Name: "ДокСтроки", Kind: metadata.KindDocument, Fields: []metadata.Field{{Name: "Период", Type: metadata.FieldTypeString}}}},
		Registers:   []*metadata.Register{{Name: "Движения", Resources: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}}}},
		InfoRegs:    []*metadata.InfoRegister{{Name: "Непериодический", Resources: []metadata.Field{{Name: "Метка", Type: metadata.FieldTypeString}}}, {Name: "Периодический", Periodic: true, Dimensions: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}}}},
		AccountRegs: []*metadata.AccountRegister{{Name: "Проводки", Resources: []metadata.Field{{Name: "Сумма", Type: metadata.FieldTypeNumber}}}},
	}
	for _, text := range []string{
		`ВЫБРАТЬ Период ИЗ Справочник.Строки ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Период ИЗ Документ.ДокСтроки`,
		`ВЫБРАТЬ Период ИЗ РегистрСведений.Непериодический ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Период ИЗ РегистрСведений.Непериодический`,
		`ВЫБРАТЬ Период ИЗ РегистрСведений.Непериодический.СрезПоследних() ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Период ИЗ РегистрСведений.Непериодический.СрезПервых()`,
		`ВЫБРАТЬ Период ИЗ РегистрБухгалтерии.Проводки ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Период ИЗ РегистрБухгалтерии.Проводки`,
		`ВЫБРАТЬ Период ИЗ РегистрНакопления.Движения.Остатки() ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Период ИЗ РегистрНакопления.Движения.Обороты()`,
		`ВЫБРАТЬ С.Период ИЗ Справочник.Строки КАК С СОЕДИНЕНИЕ РегистрСведений.Периодический КАК Р ПО Р.Ключ = С.Период ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ С.Период ИЗ Справочник.Строки КАК С`,
		`ВЫБРАТЬ Период ИЗ Документ.ДокСтроки ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Ключ ИЗ РегистрСведений.Периодический`,
		`ВЫБРАТЬ Период ИЗ Справочник.Строки КАК С СОЕДИНЕНИЕ РегистрСведений.Периодический КАК Р ПО Р.Ключ = С.Период ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Период ИЗ Справочник.Строки`,
	} {
		t.Run(text, func(t *testing.T) {
			res, err := query.Compile(text, opts)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.UnconvertedTypedFields) != 0 {
				t.Fatalf("invented period: %v", res.UnconvertedTypedFields)
			}
		})
	}
}
