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
	"github.com/stretchr/testify/require"
)

// Compare public normalization with the driver's actual last same-named value.
// An expression's unknown type must never borrow a neighbouring field's type.
func TestRunUnaliasedScalarOutputCollisionMatrix(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)
	saved := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = saved })
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		doc := &metadata.Entity{Name: "Review2034ScalarProbe", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Число", Type: metadata.FieldTypeNumber},
			{Name: "Момент", Type: metadata.FieldTypeDate},
			{Name: "Активен", Type: metadata.FieldTypeBool},
		}}
		entities := []*metadata.Entity{doc}
		require.NoError(t, db.Migrate(ctx, entities))
		for i, value := range []string{"100", "20", ""} {
			id := uuid.New()
			var number, stamp, flag any
			if i < 2 {
				number = value
				stamp = time.Date(2026, 1, 31, 23, 30, 0, 0, time.UTC)
				flag = i == 0
			}
			require.NoError(t, db.Upsert(ctx, doc.Name, id, map[string]any{"Номер": fmt.Sprint(i), "Число": number, "Момент": stamp, "Активен": flag}, doc))
			require.NoError(t, db.SetPosted(ctx, doc.Name, id, i == 0))
		}
		compile := func(t *testing.T, text string) query.Result {
			t.Helper()
			r, err := query.Compile(text, query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
			require.NoError(t, err, text)
			return r
		}
		for _, tc := range []struct{ fn, arg, key string }{
			{"ЕстьNULL", "42, 0", "coalesce"}, {"ISNULL", "42, 0", "coalesce"}, {"COALESCE", "42, 0", "coalesce"},
			{"АБС", "-42", "abs"}, {"ABS", "-42", "abs"},
		} {
			for _, wrapped := range []bool{false, true} {
				for _, reversed := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/wrapped=%v/reversed=%v", tc.fn, wrapped, reversed), func(t *testing.T) {
						expr := tc.fn + "(" + tc.arg + ")"
						if wrapped {
							expr = "(((" + expr + ")))"
						}
						projection, exprIndex := "Д.Проведен КАК "+tc.key+", "+expr, 1
						if reversed {
							projection, exprIndex = expr+", Д.Проведен КАК "+tc.key, 0
						}
						text := "ВЫБРАТЬ " + projection + " ИЗ Документ.Review2034ScalarProbe КАК Д УПОРЯДОЧИТЬ ПО Д.Номер"
						r := compile(t, text)
						raw, cols, err := db.RunQuery(ctx, r.SQL, r.Args)
						require.NoError(t, err, r.SQL)
						rows, actualCols, err := query.Run(ctx, db, &r)
						require.NoError(t, err, r.SQL)
						require.Equal(t, cols, actualCols)
						require.Len(t, rows, 3)
						if db.Dialect().Name() == "postgres" {
							require.Equal(t, []string{tc.key, tc.key}, cols)
							require.Equal(t, raw, rows, "same-name SQL values and Go types must survive")
							require.Empty(t, r.BoolColumns)
							if !reversed {
								require.EqualValues(t, 42, rows[0][tc.key])
							}
						} else {
							require.Equal(t, []string{tc.key}, r.BoolColumns)
							require.Equal(t, true, rows[0][tc.key])
							require.Equal(t, false, rows[1][tc.key])
							require.Equal(t, int64(42), rows[0][cols[exprIndex]])
						}
						// Cross a derived boundary: PostgreSQL must retain its ambiguous error.
						r = compile(t, "ВЫБРАТЬ X."+tc.key+" КАК Флаг ИЗ ("+text+") КАК X")
						rows, _, err = query.Run(ctx, db, &r)
						if db.Dialect().Name() == "postgres" {
							require.Empty(t, r.BoolColumns)
							require.ErrorContains(t, err, "ambiguous", r.SQL)
						} else {
							require.NoError(t, err, r.SQL)
							require.Equal(t, true, rows[0]["флаг"])
						}
					})
				}
			}
		}
		t.Run("unique aliases across derived boundaries", func(t *testing.T) {
			for _, expr := range []string{"ЕстьNULL(42,0)", "ISNULL(42,0)", "COALESCE(42,0)", "АБС(-42)", "ABS(-42)", "ОКР(42,0)", "ROUND(42,0)"} {
				for _, wrapped := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/wrapped=%v", expr, wrapped), func(t *testing.T) {
						if wrapped {
							expr = "(((" + expr + ")))"
						}
						inner := "ВЫБРАТЬ Д.Номер, Д.Активен КАК Флаг, Д.Число КАК Числовое, Д.Момент КАК ДатаДляКалендаря, " + expr + " ИЗ Документ.Review2034ScalarProbe КАК Д"
						source := "(ВЫБРАТЬ X.Номер, X.Флаг, X.Числовое, X.ДатаДляКалендаря ИЗ (" + inner + ") КАК X) КАК Y"
						r := compile(t, "ВЫБРАТЬ Y.Флаг, День(Y.ДатаДляКалендаря) КАК День ИЗ "+source+" УПОРЯДОЧИТЬ ПО Y.Номер")
						rows, _, err := query.Run(ctx, db, &r)
						require.NoError(t, err, r.SQL)
						require.Len(t, rows, 3)
						require.Equal(t, true, rows[0]["флаг"])
						require.Equal(t, false, rows[1]["флаг"])
						require.Nil(t, rows[2]["флаг"])
						require.EqualValues(t, 1, rows[0]["день"])
						require.Nil(t, rows[2]["день"])
						r = compile(t, "ВЫБРАТЬ МИН(Y.Числовое) КАК Мин, МАКС(Y.Числовое) КАК Макс ИЗ "+source)
						rows, _, err = query.Run(ctx, db, &r)
						require.NoError(t, err, r.SQL)
						require.Equal(t, "20", moneyDecimal(t, rows[0]["мин"]).String())
						_, minIsText := rows[0]["мин"].(string)
						require.False(t, minIsText)
						require.Equal(t, "100", moneyDecimal(t, rows[0]["макс"]).String())
						_, maxIsText := rows[0]["макс"].(string)
						require.False(t, maxIsText)
					})
				}
			}
		})
		for _, tc := range []struct{ expr, key string }{{"ЕстьNULL(42,0)", "coalesce"}, {"ABS(-42)", "abs"}} {
			for _, reversed := range []bool{false, true} {
				for _, field := range []string{"Число", "Момент"} {
					t.Run(fmt.Sprintf("scalar export/%s/%s/reversed=%v", tc.key, field, reversed), func(t *testing.T) {
						projection := "Д." + field + " КАК " + tc.key + ", " + tc.expr
						if reversed {
							projection = tc.expr + ", Д." + field + " КАК " + tc.key
						}
						outer := "МИН(X." + tc.key + ") КАК Значение"
						if field == "Момент" {
							outer = "День(X." + tc.key + ") КАК Значение"
						}
						r := compile(t, "ВЫБРАТЬ "+outer+" ИЗ (ВЫБРАТЬ "+projection+" ИЗ Документ.Review2034ScalarProbe КАК Д) КАК X")
						rows, _, err := query.Run(ctx, db, &r)
						if db.Dialect().Name() == "postgres" {
							require.ErrorContains(t, err, "ambiguous", r.SQL)
						} else {
							require.NoError(t, err, r.SQL)
							if field == "Число" {
								require.EqualValues(t, 20, rows[0]["значение"])
							} else {
								require.EqualValues(t, 1, rows[0]["значение"])
							}
						}
					})
				}
			}
		}
		t.Run("several collisions and independent nullable bool", func(t *testing.T) {
			r := compile(t, "ВЫБРАТЬ Д.Проведен КАК coalesce, ЕстьNULL(42,0), COALESCE(43,0), Д.Проведен КАК abs, ABS(-44), Д.Активен КАК Флаг ИЗ Документ.Review2034ScalarProbe КАК Д УПОРЯДОЧИТЬ ПО Д.Номер")
			rows, _, err := query.Run(ctx, db, &r)
			require.NoError(t, err, r.SQL)
			require.Equal(t, true, rows[0]["флаг"])
			require.Equal(t, false, rows[1]["флаг"])
			require.Nil(t, rows[2]["флаг"])
			if db.Dialect().Name() == "postgres" {
				require.EqualValues(t, 43, rows[0]["coalesce"])
				require.EqualValues(t, 44, rows[0]["abs"])
			}
		})
		t.Run("unproved output name", func(t *testing.T) {
			for _, expr := range []string{"NULLIF(42,0)", "NULLIF(NULL,0)"} {
				r := compile(t, "ВЫБРАТЬ Д.Проведен КАК nullif, "+expr+" ИЗ Документ.Review2034ScalarProbe КАК Д УПОРЯДОЧИТЬ ПО Д.Номер")
				raw, _, err := db.RunQuery(ctx, r.SQL, r.Args)
				require.NoError(t, err, r.SQL)
				rows, _, err := query.Run(ctx, db, &r)
				require.NoError(t, err, r.SQL)
				require.Empty(t, r.BoolColumns)
				require.Equal(t, raw, rows)
				if db.Dialect().Name() == "postgres" {
					if expr == "NULLIF(42,0)" {
						require.EqualValues(t, 42, rows[0]["nullif"])
					} else {
						require.Nil(t, rows[0]["nullif"])
					}
				}
			}
			// Unknown names also prevent a type from crossing a derived wildcard.
			r := compile(t, "ВЫБРАТЬ X.*, Д.Проведен КАК nullif ИЗ (ВЫБРАТЬ NULLIF(42,0) ИЗ Документ.Review2034ScalarProbe) КАК X ЛЕВОЕ СОЕДИНЕНИЕ Документ.Review2034ScalarProbe КАК Д ПО ИСТИНА")
			raw, _, err := db.RunQuery(ctx, r.SQL, r.Args)
			require.NoError(t, err, r.SQL)
			rows, _, err := query.Run(ctx, db, &r)
			require.NoError(t, err, r.SQL)
			require.Empty(t, r.BoolColumns)
			require.Equal(t, raw, rows)
			// Unknown output names cannot export a sibling number or date type.
			r = compile(t, "ВЫБРАТЬ МИН(X.Числовое) КАК Мин, МАКС(X.Числовое) КАК Макс ИЗ (ВЫБРАТЬ Д.Число КАК Числовое, NULLIF(42,0) ИЗ Документ.Review2034ScalarProbe КАК Д) КАК X")
			rows, _, err = query.Run(ctx, db, &r)
			require.NoError(t, err, r.SQL)
			if db.Dialect().Name() == "sqlite" {
				require.Equal(t, "100", rows[0]["мин"])
				require.Equal(t, "20", rows[0]["макс"])
			}
			r = compile(t, "ВЫБРАТЬ День(X.Момент) КАК День ИЗ (ВЫБРАТЬ Д.Момент, NULLIF(42,0) ИЗ Документ.Review2034ScalarProbe КАК Д) КАК X")
			rows, _, err = query.Run(ctx, db, &r)
			require.NoError(t, err, r.SQL)
			if db.Dialect().Name() == "sqlite" {
				require.EqualValues(t, 31, rows[0]["день"])
			}
		})

	})
}
