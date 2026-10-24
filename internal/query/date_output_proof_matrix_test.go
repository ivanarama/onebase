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
	"github.com/stretchr/testify/require"
)

// Exercise the public compiler/runner across the integration boundary: dates
// from JOIN and MIN/MAX must coexist with proven flags/numbers, while rejected
// output keys must not acquire a date through either normalization path.
func TestRunDateOutputProofMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		moment := time.Date(2026, 10, 18, 14, 30, 13, 0, time.UTC)
		doc := &metadata.Entity{Name: "DateProofDocument", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Момент", Type: metadata.FieldTypeDate},
			{Name: "Текст", Type: metadata.FieldTypeString},
			{Name: "Число", Type: metadata.FieldTypeNumber},
			{Name: "Флаг", Type: metadata.FieldTypeBool},
		}}
		joined := &metadata.Entity{Name: "DateProofJoined", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			// Same logical field name, a different source type.
			{Name: "Текст", Type: metadata.FieldTypeDate},
		}}
		ents := []*metadata.Entity{doc, joined}
		require.NoError(t, db.Migrate(ctx, ents))
		require.NoError(t, db.Upsert(ctx, joined.Name, uuid.New(), map[string]any{"Текст": moment}, joined))
		for i, n := range []string{"100", "20", ""} {
			var date, number, flag any
			if i < 2 {
				date = moment
				number = n
				flag = i == 0
			}
			id := uuid.New()
			require.NoError(t, db.Upsert(ctx, doc.Name, id, map[string]any{
				"Номер": fmt.Sprint(i), "Момент": date, "Текст": moment.Format(time.RFC3339), "Число": number, "Флаг": flag,
			}, doc))
			require.NoError(t, db.SetPosted(ctx, doc.Name, id, i == 0))
		}
		source := "Документ.DateProofDocument КАК Д ЛЕВОЕ СОЕДИНЕНИЕ Справочник.DateProofJoined КАК Ж ПО ИСТИНА"
		compile := func(t *testing.T, text string) query.Result {
			t.Helper()
			r, err := query.Compile(text, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
			require.NoError(t, err, text)
			return r
		}
		run := func(t *testing.T, text string) (query.Result, []map[string]any) {
			t.Helper()
			r := compile(t, text)
			rows, _, err := query.Run(ctx, db, &r)
			require.NoError(t, err, r.SQL)
			return r, rows
		}
		dateValue := func(t *testing.T, v any) {
			t.Helper()
			d, ok := v.(time.Time)
			require.True(t, ok, "date has type %T (%v)", v, v)
			require.True(t, moment.Equal(d), "%v != %v", moment, d)
		}
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("joined/reverse=%v", reverse), func(t *testing.T) {
				projection := "Ж.Текст КАК Дата, Д.Проведен КАК П, Д.ПометкаУдаления КАК У, Д.Флаг КАК Ф, Д.Текст КАК Строка"
				if reverse {
					projection = "Д.Текст КАК Строка, Д.Флаг КАК Ф, Д.ПометкаУдаления КАК У, Д.Проведен КАК П, Ж.Текст КАК Дата"
				}
				r, rows := run(t, "ВЫБРАТЬ "+projection+" ИЗ "+source+" УПОРЯДОЧИТЬ ПО Д.Номер")
				require.Equal(t, []string{"дата"}, r.DateColumns)
				require.Len(t, rows, 3)
				for i, row := range rows {
					dateValue(t, row["дата"])
					require.Equal(t, moment.Format(time.RFC3339), row["строка"])
					require.Equal(t, i == 0, row["п"])
					require.Equal(t, false, row["у"])
					if i == 2 {
						require.Nil(t, row["ф"])
					} else {
						require.Equal(t, i == 0, row["ф"])
					}
				}
			})
			t.Run(fmt.Sprintf("aggregate/reverse=%v", reverse), func(t *testing.T) {
				projection := "МИН(Ж.Текст) КАК Первая, МАКС(Ж.Текст) КАК Последняя, МИН(Д.Число) КАК Мин, МАКС(Д.Число) КАК Макс, Д.Проведен КАК П"
				if reverse {
					projection = "Д.Проведен КАК П, МАКС(Д.Число) КАК Макс, МИН(Д.Число) КАК Мин, МАКС(Ж.Текст) КАК Последняя, МИН(Ж.Текст) КАК Первая"
				}
				r, rows := run(t, "ВЫБРАТЬ "+projection+" ИЗ "+source+" СГРУППИРОВАТЬ ПО Д.Проведен")
				want := []string{"первая", "последняя"}
				if reverse {
					want = []string{"последняя", "первая"}
				}
				require.Equal(t, want, r.DateColumns)
				require.Len(t, rows, 2)
				for _, row := range rows {
					dateValue(t, row["первая"])
					dateValue(t, row["последняя"])
					require.IsType(t, false, row["п"])
				}
			})
		}
		t.Run("numeric and string extrema", func(t *testing.T) {
			_, rows := run(t, "ВЫБРАТЬ МИН(Д.Число) КАК Мин, МАКС(Д.Число) КАК Макс, МИН(Ж.Текст) КАК Дата ИЗ "+source)
			require.Equal(t, "20", fmt.Sprint(rows[0]["мин"]))
			require.Equal(t, "100", fmt.Sprint(rows[0]["макс"]))
			dateValue(t, rows[0]["дата"])
		})
		t.Run("null left join", func(t *testing.T) {
			_, rows := run(t, "ВЫБРАТЬ Ж.Текст КАК Дата, Д.Флаг КАК Ф ИЗ Документ.DateProofDocument КАК Д ЛЕВОЕ СОЕДИНЕНИЕ Справочник.DateProofJoined КАК Ж ПО 1=0 УПОРЯДОЧИТЬ ПО Д.Номер")
			require.Len(t, rows, 3)
			for _, row := range rows {
				require.Nil(t, row["дата"])
			}
			require.Nil(t, rows[2]["ф"])
		})
		t.Run("derived calendar and numeric consumers", func(t *testing.T) {
			inner := "ВЫБРАТЬ Д.Номер, Ж.Текст КАК Момент, Д.Флаг, Д.Число ИЗ " + source
			_, rows := run(t, "ВЫБРАТЬ День(X.Момент) КАК День, X.Флаг КАК Ф ИЗ ("+inner+") КАК X УПОРЯДОЧИТЬ ПО X.Номер")
			require.Len(t, rows, 3)
			for _, row := range rows {
				require.EqualValues(t, 18, row["день"])
			}
			require.Equal(t, true, rows[0]["ф"])
			require.Equal(t, false, rows[1]["ф"])
			require.Nil(t, rows[2]["ф"])
			_, rows = run(t, "ВЫБРАТЬ МИН(X.Число) КАК Мин, МАКС(X.Число) КАК Макс ИЗ ("+inner+") КАК X")
			require.Equal(t, "20", fmt.Sprint(rows[0]["мин"]))
			require.Equal(t, "100", fmt.Sprint(rows[0]["макс"]))
			for _, projection := range []string{"Д.Момент КАК Значение, Д.Текст КАК Значение", "Д.Текст КАК Значение, Д.Момент КАК Значение"} {
				r := compile(t, "ВЫБРАТЬ День(X.Значение) КАК День ИЗ (ВЫБРАТЬ "+projection+" ИЗ Документ.DateProofDocument КАК Д) КАК X")
				require.NotContains(t, r.SQL, "ob_local_datetime(")
				rows, _, err := query.Run(ctx, db, &r)
				if db.Dialect().Name() == "postgres" {
					require.ErrorContains(t, err, "ambiguous", r.SQL)
				} else {
					require.NoError(t, err, r.SQL)
					require.Len(t, rows, 3)
				}
			}
		})
		t.Run("register physical date key", func(t *testing.T) {
			reg := &metadata.Register{Name: "DateProofRegister"}
			require.NoError(t, db.MigrateRegisters(ctx, []*metadata.Register{reg}))
			require.NoError(t, db.WriteMovements(ctx, reg.Name, doc.Name, uuid.New(), []map[string]any{{"ВидДвижения": "Приход"}}, reg, &moment))
			opts := query.CompileOpts{Registers: []*metadata.Register{reg}, Dialect: db.Dialect()}
			for _, projection := range []string{"Период", "Р.Период", "Период КАК Момент"} {
				r, err := query.Compile("ВЫБРАТЬ "+projection+" ИЗ РегистрНакопления.DateProofRegister КАК Р", opts)
				require.NoError(t, err)
				rows, cols, err := query.Run(ctx, db, &r)
				require.NoError(t, err, r.SQL)
				require.Len(t, rows, 1)
				require.Equal(t, cols, r.DateColumns)
				dateValue(t, rows[0][cols[0]])
			}
		})

		for _, alias := range []string{"Значение", "Ссылка", "Reference", "Ref"} {
			t.Run("reserved/"+alias, func(t *testing.T) {
				r, rows := run(t, "ВЫБРАТЬ Ж.Текст КАК "+alias+" ИЗ "+source)
				key := "значение"
				if alias != "Значение" {
					key = "id"
				}
				require.Equal(t, []string{key}, r.DateColumns)
				for _, row := range rows {
					dateValue(t, row[key])
				}
			})
		}
		t.Run("long physical output collision", func(t *testing.T) {
			const name = "ДлинныйПсевдонимДатыКоторыйНельзяПутатьССоседнейСтрокой"
			short := metadata.SQLIdent(strings.ToLower(name))
			for _, projection := range []string{
				"Д.Момент КАК " + name + ", Д.Текст КАК " + short,
				"Д.Текст КАК " + short + ", Д.Момент КАК " + name,
			} {
				r := compile(t, "ВЫБРАТЬ "+projection+" ИЗ "+source+" УПОРЯДОЧИТЬ ПО Д.Номер")
				raw, cols, err := db.RunQuery(ctx, r.SQL, r.Args)
				require.NoError(t, err, r.SQL)
				rows, actual, err := query.Run(ctx, db, &r)
				require.NoError(t, err, r.SQL)
				require.Empty(t, r.DateColumns)
				require.Equal(t, cols, actual)
				require.Equal(t, raw, rows)
			}
		})

		for _, src := range []string{"Документ.DateProofDocument КАК Д", source} {
			for _, projection := range []string{
				"Д.Момент КАК Значение, Д.Текст КАК Значение",
				"Д.Текст КАК Значение, Д.Момент КАК Значение",
				"Д.Момент КАК Ссылка, Д.Текст КАК Ref",
				"Д.Текст КАК Reference, Д.Момент КАК id",
				"Д.Момент КАК Момент, Д.Текст КАК Момент",
				"Д.Момент КАК nullif, NULLIF(42,0)",
			} {
				t.Run("collision/"+src+"/"+projection, func(t *testing.T) {
					r := compile(t, "ВЫБРАТЬ "+projection+" ИЗ "+src+" УПОРЯДОЧИТЬ ПО Д.Номер")
					raw, cols, err := db.RunQuery(ctx, r.SQL, r.Args)
					require.NoError(t, err, r.SQL)
					rows, actual, err := query.Run(ctx, db, &r)
					require.NoError(t, err, r.SQL)
					require.Equal(t, cols, actual)
					require.Empty(t, r.DateColumns)
					require.Equal(t, raw, rows, "both values and driver Go types must remain unchanged")
				})
			}
		}
		for _, expr := range []struct{ key, text string }{
			{"count", "COUNT(*)"}, {"coalesce", "COALESCE(42,0)"}, {"abs", "ABS(-42)"},
		} {
			for _, reverse := range []bool{false, true} {
				t.Run(fmt.Sprintf("implicit/%s/reverse=%v", expr.key, reverse), func(t *testing.T) {
					projection := "МИН(Д.Момент) КАК " + expr.key + ", " + expr.text
					if reverse {
						projection = expr.text + ", МИН(Д.Момент) КАК " + expr.key
					}
					r := compile(t, "ВЫБРАТЬ "+projection+" ИЗ "+source)
					raw, cols, err := db.RunQuery(ctx, r.SQL, r.Args)
					require.NoError(t, err, r.SQL)
					rows, _, err := query.Run(ctx, db, &r)
					require.NoError(t, err, r.SQL)
					if db.Dialect().Name() == "postgres" {
						require.Empty(t, r.DateColumns)
						require.Equal(t, raw, rows)
					} else {
						require.Equal(t, []string{expr.key}, r.DateColumns)
						dateValue(t, rows[0][expr.key])
						for _, col := range cols {
							if col != expr.key {
								require.Equal(t, raw[0][col], rows[0][col])
							}
						}
					}
				})
			}
		}
	})
}
