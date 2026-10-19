package query_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Execute the public compiler/runner on both databases: SQL text alone cannot
// prove the exported names or the meaning of GROUP/ORDER/WHERE/HAVING.
func TestRegisterOutputAliasMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := &metadata.InfoRegister{Name: "АлиасыПериода", Periodic: true,
			Dimensions: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Метка", Type: metadata.FieldTypeString}}}
		entity := &metadata.Entity{Name: "КоллизияПериода", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Период", Type: metadata.FieldTypeString}}}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		for i, label := range []string{"z", "a"} {
			period := time.Date(2026, 10, 14+i, 12, 0, 0, 0, time.UTC)
			if err := db.InfoRegSet(ctx, ir, map[string]any{"Ключ": label}, map[string]any{"Метка": label}, &period); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{"Период": "entity"}, entity); err != nil {
			t.Fatal(err)
		}
		const from = " ИЗ РегистрСведений.АлиасыПериода КАК Р"
		for _, tc := range []struct {
			name, src string
			cols      []string
			want      []string
		}{
			{"direct", "ВЫБРАТЬ Р.period КАК Период" + from + " УПОРЯДОЧИТЬ ПО Период", []string{"период"}, []string{"2026-10-14T12:00:00Z", "2026-10-15T12:00:00Z"}},
			{"mixed case English AS", "SELECT Р.period AS пЕрИоД" + from + " ORDER BY ПЕРИОД", []string{"период"}, []string{"2026-10-14T12:00:00Z", "2026-10-15T12:00:00Z"}},
			{"Russian source", "ВЫБРАТЬ Р.Период КАК Период" + from + " УПОРЯДОЧИТЬ ПО Период", []string{"период"}, []string{"2026-10-14T12:00:00Z", "2026-10-15T12:00:00Z"}},
			{"Latin output unchanged", "ВЫБРАТЬ Р.Период AS period" + from + " ORDER BY period", []string{"period"}, []string{"2026-10-14T12:00:00Z", "2026-10-15T12:00:00Z"}},
			{"implicit system column", "ВЫБРАТЬ Р.Период" + from + " ORDER BY Р.Период", []string{"period"}, []string{"2026-10-14T12:00:00Z", "2026-10-15T12:00:00Z"}},
			{"ordinary alias", "ВЫБРАТЬ Р.period КАК Момент" + from + " ORDER BY Момент", []string{"момент"}, []string{"2026-10-14T12:00:00Z", "2026-10-15T12:00:00Z"}},
			{"aggregate", "ВЫБРАТЬ МАКСИМУМ(Р.period) КАК Период" + from, []string{"период"}, []string{"2026-10-15T12:00:00Z"}},
			{"aggregate group order", "SELECT MAX(Р.period) AS Период" + from + " GROUP BY Р.Ключ ORDER BY Период", []string{"период"}, []string{"2026-10-14T12:00:00Z", "2026-10-15T12:00:00Z"}},
			{"expression grouping alias", "ВЫБРАТЬ (Р.period) КАК Период" + from + " GROUP BY Период ORDER BY Период", []string{"период"}, []string{"2026-10-14T12:00:00Z", "2026-10-15T12:00:00Z"}},
			{"order output wins", "ВЫБРАТЬ Р.Метка КАК Период" + from + " ORDER BY Период", []string{"период"}, []string{"a", "z"}},
			{"qualified order source wins", "ВЫБРАТЬ Р.Метка КАК Период" + from + " ORDER BY Р.Период", []string{"период"}, []string{"z", "a"}},
			{"where source wins", "ВЫБРАТЬ Р.Метка КАК Период" + from + " WHERE Период < &Граница", []string{"период"}, []string{"z"}},
			{"having source wins", "ВЫБРАТЬ МИНИМУМ(Р.Метка) КАК Период" + from + " GROUP BY Р.Период HAVING Период < &Граница", []string{"период"}, []string{"z"}},
			{"group output wins", "ВЫБРАТЬ Р.Метка КАК Период" + from + " GROUP BY Период ORDER BY Период", []string{"период"}, []string{"a", "z"}},
			{"derived output", "ВЫБРАТЬ Т.Период ИЗ (ВЫБРАТЬ Р.period КАК Период" + from + ") КАК Т ORDER BY Т.Период", []string{"период"}, []string{"2026-10-14T12:00:00Z", "2026-10-15T12:00:00Z"}},
			{"derived bare output", "ВЫБРАТЬ Период ИЗ (ВЫБРАТЬ Р.period КАК Период" + from + ") КАК Т ORDER BY Период", []string{"период"}, []string{"2026-10-14T12:00:00Z", "2026-10-15T12:00:00Z"}},
			{"derived aggregate", "ВЫБРАТЬ Т.Период ИЗ (ВЫБРАТЬ MAX(Р.period) КАК Период" + from + ") КАК Т", []string{"период"}, []string{"2026-10-15T12:00:00Z"}},
			{"derived implicit system", "ВЫБРАТЬ Т.Период ИЗ (ВЫБРАТЬ Р.Период" + from + ") КАК Т ORDER BY Т.Период", []string{"period"}, []string{"2026-10-14T12:00:00Z", "2026-10-15T12:00:00Z"}},
			{"derived explicit beside implicit", "ВЫБРАТЬ Т.Период ИЗ (ВЫБРАТЬ Р.Период, Р.Метка КАК Период" + from + ") КАК Т ORDER BY Т.Период", []string{"период"}, []string{"a", "z"}},
			{"two derived levels", "ВЫБРАТЬ П.Период ИЗ (ВЫБРАТЬ Т.Период ИЗ (ВЫБРАТЬ Р.period КАК Период" + from + ") КАК Т) КАК П ORDER BY П.Период", []string{"период"}, []string{"2026-10-14T12:00:00Z", "2026-10-15T12:00:00Z"}},
			{"inner alias does not hide outer source", "ВЫБРАТЬ Р.Период" + from + " WHERE Р.Ключ IN (ВЫБРАТЬ Ключ КАК Период ИЗ РегистрСведений.АлиасыПериода) ORDER BY Период", []string{"period"}, []string{"2026-10-14T12:00:00Z", "2026-10-15T12:00:00Z"}},
			{"outer alias does not hide inner source", "ВЫБРАТЬ Р.Метка КАК Период" + from + " WHERE Р.Ключ IN (ВЫБРАТЬ Ключ ИЗ РегистрСведений.АлиасыПериода WHERE Период < &Граница)", []string{"период"}, []string{"z"}},
			{"union order first scope", "ВЫБРАТЬ Р.Метка КАК Период" + from + " WHERE Р.Ключ = 'z' UNION ALL SELECT Р.Метка" + from + " WHERE Р.Ключ = 'a' ORDER BY Период", []string{"период"}, []string{"a", "z"}},
			{"entity field collision", "ВЫБРАТЬ П.Период КАК Период ИЗ Справочник.КоллизияПериода КАК П", []string{"период"}, []string{"entity"}},
			{"entity field joins register", "ВЫБРАТЬ П.Период КАК Период" + from + " INNER JOIN Справочник.КоллизияПериода КАК П ON Р.Ключ = 'z'", []string{"период"}, []string{"entity"}},
			{"source qualifier matches system name", "ВЫБРАТЬ Период.Метка КАК Период ИЗ РегистрСведений.АлиасыПериода КАК Период ORDER BY Период", []string{"период"}, []string{"a", "z"}},
			{"reserved reference alias", "ВЫБРАТЬ Р.Метка КАК Ссылка" + from + " ORDER BY Ссылка", []string{"id"}, []string{"a", "z"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				res, err := query.Compile(tc.src, query.CompileOpts{InfoRegs: []*metadata.InfoRegister{ir},
					Entities: []*metadata.Entity{entity}, Dialect: db.Dialect(),
					Params: map[string]any{"Граница": time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)}})
				if err != nil {
					t.Fatal(err)
				}
				rows, cols, err := query.Run(ctx, db, &res)
				if err != nil {
					t.Fatalf("%v; SQL=%s", err, res.SQL)
				}
				if !reflect.DeepEqual(cols, tc.cols) || len(rows) != len(tc.want) {
					t.Fatalf("columns=%v rows=%v, want %v %v; SQL=%s", cols, rows, tc.cols, tc.want, res.SQL)
				}
				for i, row := range rows {
					value := fmt.Sprint(row[tc.cols[0]])
					if date, ok := row[tc.cols[0]].(time.Time); ok {
						value = date.UTC().Format(time.RFC3339)
					} else {
						for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05Z07:00"} {
							if date, err := time.Parse(layout, value); err == nil {
								value = date.UTC().Format(time.RFC3339)
								break
							}
						}
					}
					if value != tc.want[i] {
						t.Fatalf("row=%v, want %q; SQL=%s", row, tc.want[i], res.SQL)
					}
				}
			})
		}
	})
}
