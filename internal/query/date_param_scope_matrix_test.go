package query_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Формат параметра определяется полем его SELECT/квалификатора, даже если
// первый источник всего запроса хранит даты иначе (регресс ревью PR #1833).
func TestDateParamUsesComparedFieldScopeMatrix(t *testing.T) {
	loc := time.FixedZone("Europe/Moscow", 3*60*60)
	boundary := time.Date(2026, 10, 10, 12, 0, 0, 0, loc)
	upper := boundary.Add(2 * time.Hour)
	ent := &metadata.Entity{Name: "Событие", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Метка", Type: metadata.FieldTypeString},
		{Name: "Момент", Type: metadata.FieldTypeDate},
	}}
	reg := &metadata.Register{Name: "Долги",
		Dimensions: []metadata.Field{{Name: "Метка", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Сумма", Type: metadata.FieldTypeNumber}},
		Attributes: []metadata.Field{{Name: "Момент", Type: metadata.FieldTypeDate}},
	}

	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		require.NoError(t, db.Migrate(ctx, []*metadata.Entity{ent}))
		require.NoError(t, db.MigrateRegisters(ctx, []*metadata.Register{reg}))
		for _, point := range []struct {
			label  string
			moment time.Time
		}{{"до", boundary.Add(-time.Hour)}, {"после", boundary.Add(time.Hour)}} {
			require.NoError(t, db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{
				"Метка": "с:" + point.label, "Момент": point.moment,
			}, ent))
			moment := point.moment
			require.NoError(t, db.WriteMovements(ctx, reg.Name, "Документ", uuid.New(), []map[string]any{
				{"ВидДвижения": "Приход", "Метка": "р:" + point.label, "Сумма": float64(1), "Момент": moment},
			}, reg, &moment))
		}

		for _, field := range []string{"Момент", "С.Момент"} {
			for _, condition := range []string{
				field + " >= &Граница",
				"&Граница <= " + field,
				field + " МЕЖДУ &Граница И &Верх",
			} {
				for _, source := range []struct{ text, empty, want string }{
					{"Справочник.Событие", "РегистрНакопления.Долги", "с:после"},
					{"РегистрНакопления.Долги", "Справочник.Событие", "р:после"},
				} {
					text := "ВЫБРАТЬ Метка ИЗ " + source.empty + " ГДЕ 1 = 0 ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Метка ИЗ " + source.text + " КАК С ГДЕ " + condition
					t.Run(source.text+"/"+condition, func(t *testing.T) {
						compiled, err := query.Compile(text, query.CompileOpts{
							Entities: []*metadata.Entity{ent}, Registers: []*metadata.Register{reg},
							Dialect: db.Dialect(), Params: map[string]any{"Граница": boundary, "Верх": &upper},
						})
						require.NoError(t, err)
						rows, _, err := query.Run(ctx, db, &compiled)
						require.NoError(t, err, compiled.SQL)
						require.Len(t, rows, 1, "%s; args=%v", compiled.SQL, compiled.Args)
						require.Equal(t, source.want, fmt.Sprint(rows[0]["метка"]))
					})
				}
			}
		}

		for _, tc := range []struct {
			name, text string
			want       []string
		}{
			{"same parameter across sources", "ВЫБРАТЬ Метка ИЗ РегистрНакопления.Долги КАК С ГДЕ С.Момент >= &Граница ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Метка ИЗ Справочник.Событие КАК С ГДЕ С.Момент >= &Граница", []string{"р:после", "с:после"}},
			{"joined entity", "ВЫБРАТЬ С.Метка КАК Метка ИЗ РегистрНакопления.Долги КАК Р ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Событие КАК С ПО Р.Метка = 'р:до' ГДЕ Р.Метка = 'р:до' И С.Момент >= &Граница", []string{"с:после"}},
			{"joined register", "ВЫБРАТЬ Р.Метка КАК Метка ИЗ Справочник.Событие КАК С ЛЕВОЕ СОЕДИНЕНИЕ РегистрНакопления.Долги КАК Р ПО С.Метка = 'с:до' ГДЕ С.Метка = 'с:до' И Р.Момент >= &Граница", []string{"р:после"}},
			{"nested entity", "ВЫБРАТЬ Р.Метка КАК Метка ИЗ РегистрНакопления.Долги КАК Р ГДЕ Р.Метка = 'р:до' И 1 = (ВЫБРАТЬ КОЛИЧЕСТВО(*) ИЗ Справочник.Событие КАК С ГДЕ С.Момент >= &Граница)", []string{"р:до"}},
			{"derived entity", "ВЫБРАТЬ С.Метка КАК Метка ИЗ (ВЫБРАТЬ Метка, Момент ИЗ Справочник.Событие) КАК С ГДЕ С.Момент >= &Граница", []string{"с:после"}},
			{"derived register", "ВЫБРАТЬ С.Метка КАК Метка ИЗ (ВЫБРАТЬ Метка, Момент ИЗ РегистрНакопления.Долги) КАК С ГДЕ С.Момент >= &Граница", []string{"р:после"}},
			{"nested derived alias", "ВЫБРАТЬ С.Метка КАК Метка ИЗ (ВЫБРАТЬ Д.Метка КАК Метка, Д.Дата КАК Граница ИЗ (ВЫБРАТЬ Метка, Момент КАК Дата ИЗ Справочник.Событие) КАК Д) КАК С ГДЕ С.Граница >= &Граница", []string{"с:после"}},
			{"derived bare field between", "ВЫБРАТЬ Метка ИЗ (ВЫБРАТЬ Метка, Момент ИЗ Справочник.Событие) КАК С ГДЕ Момент МЕЖДУ &Граница И &Верх", []string{"с:после"}},
			{"derived reversed comparison", "ВЫБРАТЬ С.Метка КАК Метка ИЗ (ВЫБРАТЬ Метка, Момент ИЗ Справочник.Событие) КАК С ГДЕ &Граница <= С.Момент", []string{"с:после"}},
			{"derived star", "ВЫБРАТЬ С.Метка КАК Метка ИЗ (ВЫБРАТЬ * ИЗ Справочник.Событие) КАК С ГДЕ С.Момент >= &Граница", []string{"с:после"}},
			{"derived qualified star", "ВЫБРАТЬ С.Метка КАК Метка ИЗ (ВЫБРАТЬ Д.* ИЗ Справочник.Событие КАК Д) КАК С ГДЕ С.Момент >= &Граница", []string{"с:после"}},
			{"correlated entity", "ВЫБРАТЬ С.Метка КАК Метка ИЗ Справочник.Событие КАК С ГДЕ 1 = (ВЫБРАТЬ КОЛИЧЕСТВО(*) ИЗ РегистрНакопления.Долги КАК Р ГДЕ Р.Метка = 'р:до' И С.Момент >= &Граница)", []string{"с:после"}},
			{"correlated register", "ВЫБРАТЬ Р.Метка КАК Метка ИЗ РегистрНакопления.Долги КАК Р ГДЕ 1 = (ВЫБРАТЬ КОЛИЧЕСТВО(*) ИЗ Справочник.Событие КАК С ГДЕ С.Метка = 'с:до' И Р.Момент >= &Граница)", []string{"р:после"}},
			{"correlated derived entity", "ВЫБРАТЬ С.Метка КАК Метка ИЗ (ВЫБРАТЬ Метка, Момент ИЗ Справочник.Событие) КАК С ГДЕ 1 = (ВЫБРАТЬ КОЛИЧЕСТВО(*) ИЗ РегистрНакопления.Долги КАК Р ГДЕ Р.Метка = 'р:до' И С.Момент >= &Граница)", []string{"с:после"}},
			{"correlated two levels", "ВЫБРАТЬ С.Метка КАК Метка ИЗ Справочник.Событие КАК С ГДЕ 1 = (ВЫБРАТЬ КОЛИЧЕСТВО(*) ИЗ РегистрНакопления.Долги КАК Р ГДЕ Р.Метка = 'р:до' И 1 = (ВЫБРАТЬ КОЛИЧЕСТВО(*) ИЗ РегистрНакопления.Долги КАК П ГДЕ П.Метка = 'р:до' И С.Момент >= &Граница))", []string{"с:после"}},
			{"local register shadows outer entity", "ВЫБРАТЬ С.Метка КАК Метка ИЗ Справочник.Событие КАК С ГДЕ 1 = (ВЫБРАТЬ КОЛИЧЕСТВО(*) ИЗ РегистрНакопления.Долги КАК С ГДЕ С.Момент >= &Граница)", []string{"с:до", "с:после"}},
			{"local entity shadows outer register", "ВЫБРАТЬ С.Метка КАК Метка ИЗ РегистрНакопления.Долги КАК С ГДЕ 1 = (ВЫБРАТЬ КОЛИЧЕСТВО(*) ИЗ Справочник.Событие КАК С ГДЕ С.Момент >= &Граница)", []string{"р:до", "р:после"}},
			{"derived alias shadows outer entity", "ВЫБРАТЬ С.Метка КАК Метка ИЗ Справочник.Событие КАК С ГДЕ 1 = (ВЫБРАТЬ КОЛИЧЕСТВО(*) ИЗ (ВЫБРАТЬ Метка, Момент ИЗ РегистрНакопления.Долги) КАК С ГДЕ С.Момент >= &Граница)", []string{"с:до", "с:после"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				compiled, err := query.Compile(tc.text, query.CompileOpts{
					Entities: []*metadata.Entity{ent}, Registers: []*metadata.Register{reg},
					Dialect: db.Dialect(), Params: map[string]any{"Граница": boundary, "Верх": &upper},
				})
				require.NoError(t, err)
				rows, _, err := query.Run(ctx, db, &compiled)
				require.NoError(t, err, compiled.SQL)
				var got []string
				for _, row := range rows {
					got = append(got, fmt.Sprint(row["метка"]))
				}
				require.ElementsMatch(t, tc.want, got, "%s; args=%v", compiled.SQL, compiled.Args)
			})
		}
	})
}
