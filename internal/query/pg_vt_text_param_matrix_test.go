package query_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// #2540: the internal VT filter has its own tokens and register field types.
// Execute it without PostgreSQL's compatibility uuid-to-text cast, through
// the same public Compile/QueryAll path used by application queries.
func TestVirtualTableTextParameterMatrix(t *testing.T) {
	dbtest.ForEachDialectWithoutUUIDTextCast(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		store := &metadata.Entity{Name: "СкладВТТ", Kind: metadata.KindCatalog}
		doc := &metadata.Entity{Name: "ДокВТТ", Kind: metadata.KindDocument}
		entities := []*metadata.Entity{store, doc}
		dimensions := []metadata.Field{
			{Name: "Текст", Type: metadata.FieldTypeString},
			{Name: "Склад", Type: metadata.FieldType("reference:" + store.Name), RefEntity: store.Name},
		}
		resources := []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}}
		reg := &metadata.Register{Name: "ОстаткиВТТ", Dimensions: dimensions, Resources: resources}
		info := &metadata.InfoRegister{Name: "ЦеныВТТ", Periodic: true, Dimensions: dimensions, Resources: resources}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{info}); err != nil {
			t.Fatal(err)
		}
		id, docID := uuid.New(), uuid.New()
		for _, row := range []struct {
			entity *metadata.Entity
			id     uuid.UUID
		}{{store, id}, {doc, docID}} {
			if err := db.Upsert(ctx, row.entity.Name, row.id, map[string]any{}, row.entity); err != nil {
				t.Fatal(err)
			}
		}
		period := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		var movements []map[string]any
		for i, text := range []string{id.String(), "обычный текст", ""} {
			quantity := float64(i + 1)
			movements = append(movements, map[string]any{"ВидДвижения": "Приход", "Текст": text, "Склад": id, "Количество": quantity})
			if err := db.InfoRegSet(ctx, info, map[string]any{"Текст": text, "Склад": id}, map[string]any{"Количество": quantity}, &period); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.WriteMovements(ctx, reg.Name, doc.Name, docID, movements, reg, &period); err != nil {
			t.Fatal(err)
		}
		if db.Dialect().Name() == "postgres" {
			rows, err := db.QueryAll(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`)
			if err != nil || len(rows) != 1 || rows[0]["rolsuper"] != false {
				t.Fatalf("ordinary PostgreSQL role required: %v, %v", rows, err)
			}
			rows, err = db.QueryAll(ctx, `SELECT count(*) AS n FROM pg_cast WHERE castsource = 'uuid'::regtype AND casttarget = 'text'::regtype AND castcontext = 'i'`)
			if err != nil || len(rows) != 1 || vtoLabel(rows[0]["n"]) != "0" {
				t.Fatalf("no implicit uuid-to-text cast required: %v, %v", rows, err)
			}
		}
		for _, vt := range []struct{ name, from, value string }{
			{"balances", "РегистрНакопления.ОстаткиВТТ.Остатки(, %s)", "КоличествоОстаток"},
			{"turnovers", "РегистрНакопления.ОстаткиВТТ.Обороты(, , %s)", "КоличествоОборот"},
			{"balances_turnovers", "РегистрНакопления.ОстаткиВТТ.ОстаткиИОбороты(, , %s)", "КоличествоПриход"},
			{"first_slice", "РегистрСведений.ЦеныВТТ.СрезПервых(, %s)", "Количество"},
			{"last_slice", "РегистрСведений.ЦеныВТТ.СрезПоследних(, %s)", "Количество"},
		} {
			t.Run(vt.name, func(t *testing.T) {
				for _, tc := range []struct {
					name, filter string
					param        any
					want         string
				}{
					{"uuid_text", "Текст = &П", id.String(), "1"},
					{"plain_text", "Текст = &П", "обычный текст", "2"},
					{"empty_text", "Текст = &П", "", "3"},
					{"reference", "Склад = &П И Текст = \"обычный текст\"", id.String(), "2"},
					{"empty_reference", "Склад = &П", "", ""},
					{"shared_text_first", "Текст = &П И Склад = &П", id.String(), "1"},
					{"shared_reference_first", "Склад = &П И Текст = &П", id.String(), "1"},
					{"reversed_text", "&П = Текст", id.String(), "1"},
					{"uuid_list", "Текст В (&П)", []any{id.String(), uuid.NewString()}, "1"},
					{"uuid_multi_parameter_list", "Текст В (&Другой, &П)", id.String(), "1"},
					{"shared_multi_parameter_list", "Текст В (&Другой, &П) И Склад = &П", id.String(), "1"},
					{"uuid_not_in", "Текст НЕ В (&П)", id.String(), "2|3"},
				} {
					t.Run(tc.name, func(t *testing.T) {
						src := fmt.Sprintf("ВЫБРАТЬ Р.%s КАК Итог ИЗ %s КАК Р УПОРЯДОЧИТЬ ПО Итог", vt.value, fmt.Sprintf(vt.from, tc.filter))
						res, err := query.Compile(src, query.CompileOpts{Entities: entities, Registers: []*metadata.Register{reg}, InfoRegs: []*metadata.InfoRegister{info}, Params: map[string]any{"П": tc.param, "Другой": uuid.NewString()}, Dialect: db.Dialect()})
						if err != nil {
							t.Fatal(err)
						}
						rows, err := db.QueryAll(ctx, res.SQL, res.Args...)
						if err != nil {
							t.Fatalf("%v\nSQL: %s", err, res.SQL)
						}
						query.NormalizeColumns(&res, rows)
						var got []string
						for _, row := range rows {
							got = append(got, vtoLabel(row["итог"]))
						}
						if strings.Join(got, "|") != tc.want {
							t.Fatalf("got %v, want %q\nSQL: %s", got, tc.want, res.SQL)
						}
					})
				}
			})
		}
	})
}
