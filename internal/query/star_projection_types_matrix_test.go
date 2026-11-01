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

// #2034: an explicit alias beside * retains its type only when every expanded
// SQL key is known and distinct. Exercise public result values, not just plans.
func TestRunStarExplicitTypesMatrix(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)
	saved := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = saved })
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		doc := &metadata.Entity{Name: "StarTypes", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Активен", Type: metadata.FieldTypeBool},
			{Name: "Момент", Type: metadata.FieldTypeDate},
			{Name: "Число", Type: metadata.FieldTypeNumber},
			{Name: "Текст", Type: metadata.FieldTypeString},
			{Name: "Владелец", Type: metadata.FieldType("reference:StarCollision"), RefEntity: "StarCollision"},
		}}
		collision := &metadata.Entity{Name: "StarCollision", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Флаг", Type: metadata.FieldTypeString},
			{Name: "ДатаДляКалендаря", Type: metadata.FieldTypeString},
			{Name: "Значение", Type: metadata.FieldTypeString},
		}}
		unknown := &metadata.Entity{Name: "StarUnknown", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Флаг", Type: metadata.FieldTypeString}}}
		entities := []*metadata.Entity{doc, collision}
		require.NoError(t, db.Migrate(ctx, append(append([]*metadata.Entity{}, entities...), unknown)))
		moment := time.Date(2027, 1, 1, 0, 30, 0, 0, loc)
		for i, flag := range []any{true, false, nil} {
			var n, stamp any
			if i < 2 {
				n = []string{"100", "20"}[i]
				stamp = moment
			}
			require.NoError(t, db.Upsert(ctx, doc.Name, uuid.New(), map[string]any{"Номер": fmt.Sprint(i), "Активен": flag, "Момент": stamp, "Число": n, "Текст": n}, doc))
		}
		require.NoError(t, db.Upsert(ctx, collision.Name, uuid.New(), map[string]any{"Номер": "collision", "Флаг": "1", "ДатаДляКалендаря": moment.UTC().Format(time.RFC3339), "Значение": "100"}, collision))
		require.NoError(t, db.Upsert(ctx, unknown.Name, uuid.New(), map[string]any{"Флаг": "1"}, unknown))
		compile := func(t *testing.T, src string) query.Result {
			t.Helper()
			res, err := query.Compile(src, query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
			require.NoError(t, err, src)
			return res
		}
		run := func(t *testing.T, src string) []map[string]any {
			t.Helper()
			res := compile(t, src)
			rows, _, err := query.Run(ctx, db, &res)
			require.NoError(t, err, res.SQL)
			return rows
		}
		for _, modifier := range []string{"", "ALL ", "ВСЕ ", "DISTINCT ", "РАЗЛИЧНЫЕ "} {
			for _, star := range []string{"*", "Д.*"} {
				for _, first := range []bool{false, true} {
					projection := star + ", Д.Активен КАК Флаг, Д.Момент КАК ДатаДляКалендаря, Д.Число КАК Числовое, Д.Текст КАК Строковое"
					if first {
						projection = "Д.Активен КАК Флаг, Д.Момент КАК ДатаДляКалендаря, Д.Число КАК Числовое, Д.Текст КАК Строковое, " + star
					}
					source := "(ВЫБРАТЬ " + modifier + projection + " ИЗ Документ.StarTypes КАК Д) КАК X"
					t.Run(fmt.Sprintf("%s%s/first=%v", modifier, star, first), func(t *testing.T) {
						t.Run("direct bool", func(t *testing.T) {
							direct := run(t, "ВЫБРАТЬ "+modifier+projection+" ИЗ Документ.StarTypes КАК Д УПОРЯДОЧИТЬ ПО Д.Номер")
							require.Len(t, direct, 3)
							require.Equal(t, true, direct[0]["флаг"])
							require.Equal(t, false, direct[1]["флаг"])
							require.Nil(t, direct[2]["флаг"])
						})
						for _, nested := range []bool{false, true} {
							src := source
							if nested {
								src = "(ВЫБРАТЬ X.Флаг, X.ДатаДляКалендаря, X.Числовое, X.Строковое, X.Номер ИЗ " + src + ") КАК X"
							}
							rows := run(t, "ВЫБРАТЬ X.Флаг, Год(X.ДатаДляКалендаря) КАК Г, Месяц(X.ДатаДляКалендаря) КАК М, День(X.ДатаДляКалендаря) КАК Д ИЗ "+src+" УПОРЯДОЧИТЬ ПО X.Номер")
							require.Len(t, rows, 3)
							for i := 0; i < 2; i++ {
								require.Equal(t, 2027, intValue(t, rows[i]["г"]))
								require.Equal(t, 1, intValue(t, rows[i]["м"]))
								require.Equal(t, 1, intValue(t, rows[i]["д"]))
								require.Equal(t, i == 0, rows[i]["флаг"])
							}
							for _, key := range []string{"флаг", "г", "м", "д"} {
								require.Nil(t, rows[2][key])
							}
							for _, agg := range [][2]string{{"MIN", "MAX"}, {"МИН", "МАКС"}, {"МИНИМУМ", "МАКСИМУМ"}} {
								values := run(t, fmt.Sprintf("ВЫБРАТЬ %s(X.Числовое) КАК Мин, %s(X.Числовое) КАК Макс, %s(X.Строковое) КАК ТМин, %s(X.Строковое) КАК ТМакс ИЗ %s", agg[0], agg[1], agg[0], agg[1], src))
								require.Equal(t, "20", fmt.Sprint(values[0]["мин"]))
								require.Equal(t, "100", fmt.Sprint(values[0]["макс"]))
								require.Equal(t, "100", values[0]["тмин"])
								require.Equal(t, "20", values[0]["тмакс"])
							}
						}
					})
				}
			}
		}
		// A qualified star expands one occurrence, even in a self-JOIN. Bare *
		// expands both; unrelated duplicated names must not erase a unique alias.
		for _, star := range []string{"*", "Д.*", "Е.*", "Д.*, Е.*"} {
			t.Run("separate sources/"+star, func(t *testing.T) {
				rows := run(t, "ВЫБРАТЬ "+star+", Д.Активен КАК Флаг ИЗ Документ.StarTypes КАК Д ЛЕВОЕ СОЕДИНЕНИЕ Документ.StarTypes КАК Е ПО Е.Номер=Д.Номер УПОРЯДОЧИТЬ ПО Д.Номер")
				require.Len(t, rows, 3)
				require.Equal(t, true, rows[0]["флаг"])
				require.Equal(t, false, rows[1]["флаг"])
				require.Nil(t, rows[2]["флаг"])
			})
		}
		// Expanding a derived source must include its * and explicit names across
		// another boundary, rather than treating only the latter as its full layout.
		t.Run("derived wildcard source", func(t *testing.T) {
			rows := run(t, `ВЫБРАТЬ X.*, X.ИсходныйФлаг КАК Флаг ИЗ (ВЫБРАТЬ Д.*, Д.Активен КАК ИсходныйФлаг, Д.Момент КАК ДатаДляКалендаря ИЗ Документ.StarTypes КАК Д) КАК X УПОРЯДОЧИТЬ ПО X.Номер`)
			require.Equal(t, true, rows[0]["флаг"])
			require.Equal(t, false, rows[1]["флаг"])
			require.Nil(t, rows[2]["флаг"])
		})
		// Real wildcard collisions, including repeat output keys, keep the old
		// fail-closed behavior. PostgreSQL rejects ambiguous derived references.
		for _, star := range []string{"*", "С.*"} {
			for _, projection := range []string{
				star + ", Д.Активен КАК Флаг, Д.Момент КАК ДатаДляКалендаря, Д.Число КАК Значение",
				"Д.Активен КАК Флаг, Д.Момент КАК ДатаДляКалендаря, Д.Число КАК Значение, " + star,
				star + ", Д.Активен КАК Флаг, Д.Активен КАК Флаг, Д.Момент КАК ДатаДляКалендаря, Д.Число КАК Значение",
			} {
				t.Run("collision/"+projection, func(t *testing.T) {
					src := "(ВЫБРАТЬ " + projection + " ИЗ Документ.StarTypes КАК Д ЛЕВОЕ СОЕДИНЕНИЕ Документ.StarCollision КАК С ПО 1=1) КАК X"
					res := compile(t, "ВЫБРАТЬ X.Флаг, Год(X.ДатаДляКалендаря) КАК Г, MIN(X.Значение) КАК Мин ИЗ "+src+" СГРУППИРОВАТЬ ПО X.Флаг, X.ДатаДляКалендаря")
					require.Empty(t, res.BoolColumns)
					require.NotContains(t, res.SQL, "ob_local_datetime(x.датадлякалендаря)")
					require.NotContains(t, res.SQL, "CAST(x.значение AS NUMERIC)")
					rows, _, err := query.Run(ctx, db, &res)
					if db.Dialect().Name() == "postgres" {
						require.ErrorContains(t, err, "ambiguous")
						return
					}
					require.NoError(t, err, res.SQL)
					if strings.HasPrefix(projection, star) {
						require.Equal(t, "1", rows[0]["флаг"])
						require.Equal(t, 2026, intValue(t, rows[0]["г"]))
						require.Equal(t, "100", rows[0]["мин"])
					}
				})
			}
		}
		t.Run("physical reference key", func(t *testing.T) {
			res := compile(t, `ВЫБРАТЬ X.Владелец_id КАК Флаг ИЗ (ВЫБРАТЬ Д.*, Д.Активен КАК Владелец_id ИЗ Документ.StarTypes КАК Д) КАК X УПОРЯДОЧИТЬ ПО X.Номер`)
			require.Empty(t, res.BoolColumns)
			rows, _, err := query.Run(ctx, db, &res)
			if db.Dialect().Name() == "postgres" {
				require.ErrorContains(t, err, "ambiguous")
				return
			}
			require.NoError(t, err, res.SQL)
			require.Len(t, rows, 3)
			for _, row := range rows {
				require.Nil(t, row["флаг"])
			}
		})

		for _, alias := range []string{"Ссылка", "Reference", "Ref", "posted", "deletion_mark", "_version"} {
			t.Run("physical system key/"+alias, func(t *testing.T) {
				res := compile(t, "ВЫБРАТЬ Д.*, Д.Активен КАК "+alias+" ИЗ Документ.StarTypes КАК Д УПОРЯДОЧИТЬ ПО Д.Номер")
				require.Empty(t, res.BoolColumns)
				rows, _, err := query.Run(ctx, db, &res)
				require.NoError(t, err, res.SQL)
				key := strings.ToLower(alias)
				if alias == "Ссылка" || alias == "Reference" || alias == "Ref" {
					key = "id"
				}
				if db.Dialect().Name() == "sqlite" {
					require.Equal(t, int64(1), rows[0][key])
					require.Equal(t, int64(0), rows[1][key])
					require.Nil(t, rows[2][key])
				}
			})
		}
		for name, source := range map[string]string{
			"unnamed expression": "(ВЫБРАТЬ Д.*, 42 ИЗ Документ.StarTypes КАК Д) КАК X",
			"union":              "(ВЫБРАТЬ Д.* ИЗ Документ.StarTypes КАК Д ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Д.* ИЗ Документ.StarTypes КАК Д) КАК X",
		} {
			t.Run("unknown derived layout/"+name, func(t *testing.T) {
				res := compile(t, "ВЫБРАТЬ X.*, Д.Активен КАК Флаг ИЗ Документ.StarTypes КАК Д ЛЕВОЕ СОЕДИНЕНИЕ "+source+" ПО 1=1")
				require.Empty(t, res.BoolColumns)
				rows, _, err := query.Run(ctx, db, &res)
				require.NoError(t, err, res.SQL)
				require.NotEmpty(t, rows)
				if db.Dialect().Name() == "sqlite" {
					_, isBool := rows[0]["флаг"].(bool)
					require.False(t, isBool)
				}
			})
		}

		t.Run("unknown expansion", func(t *testing.T) {
			res := compile(t, `ВЫБРАТЬ С.*, Д.Активен КАК Флаг ИЗ Документ.StarTypes КАК Д ЛЕВОЕ СОЕДИНЕНИЕ Справочник.StarUnknown КАК С ПО 1=1`)
			require.Empty(t, res.BoolColumns)
			rows, _, err := query.Run(ctx, db, &res)
			require.NoError(t, err, res.SQL)
			if db.Dialect().Name() == "sqlite" {
				_, isBool := rows[0]["флаг"].(bool)
				require.False(t, isBool)
			}
		})
	})
}
