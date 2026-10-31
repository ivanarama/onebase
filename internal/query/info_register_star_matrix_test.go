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

// #2034: known information-register layouts preserve unique explicit types
// beside * across public Compile -> Run, without weakening collision guards.
func TestRunInfoRegisterStarTypesMatrix(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)
	saved := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = saved })
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		for _, periodic := range []bool{false, true} {
			t.Run(fmt.Sprintf("periodic=%v", periodic), func(t *testing.T) {
				reg := &metadata.InfoRegister{Name: fmt.Sprintf("ReviewRegStar2034%v", periodic), Periodic: periodic,
					Dimensions: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}},
					Resources: []metadata.Field{
						{Name: "Активен", Type: metadata.FieldTypeBool},
						{Name: "Момент", Type: metadata.FieldTypeDate},
						{Name: "Число", Type: metadata.FieldTypeNumber},
						{Name: "Текст", Type: metadata.FieldTypeString},
						{Name: "Владелец", Type: metadata.FieldType("reference:StarCollision"), RefEntity: "StarCollision"},
					}}
				// Exercise physical _id keys in both resources and dimensions.
				if periodic {
					reg.Dimensions = append(reg.Dimensions, reg.Resources[len(reg.Resources)-1])
					reg.Resources = reg.Resources[:len(reg.Resources)-1]
				}

				collision := &metadata.InfoRegister{Name: "StarCollision", Dimensions: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}}, Resources: []metadata.Field{
					{Name: "Флаг", Type: metadata.FieldTypeString},
					{Name: "ДатаДляКалендаря", Type: metadata.FieldTypeString},
					{Name: "Значение", Type: metadata.FieldTypeString},
				}}
				unknown := &metadata.InfoRegister{Name: "StarUnknown", Dimensions: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}}, Resources: []metadata.Field{{Name: "Флаг", Type: metadata.FieldTypeString}}}
				target := &metadata.Entity{Name: "StarCollision", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Строка", Type: metadata.FieldTypeString}}}
				require.NoError(t, db.Migrate(ctx, []*metadata.Entity{target}))
				targetID := uuid.New()
				require.NoError(t, db.Upsert(ctx, target.Name, targetID, map[string]any{"Строка": "ref"}, target))
				regs := []*metadata.InfoRegister{reg, collision}
				require.NoError(t, db.MigrateInfoRegisters(ctx, append(append([]*metadata.InfoRegister{}, regs...), unknown)))
				moment := time.Date(2027, 1, 1, 0, 30, 0, 0, loc)
				var period *time.Time
				if periodic {
					period = &moment
				}
				for i, flag := range []any{true, false, nil} {
					var n, stamp any
					if i < 2 {
						n = []string{"100", "20"}[i]
						stamp = moment
					}
					dims := map[string]any{"Ключ": fmt.Sprint(i)}
					resources := map[string]any{"Активен": flag, "Момент": stamp, "Число": n, "Текст": n}
					if periodic {
						dims["Владелец"] = targetID
					} else {
						resources["Владелец"] = targetID
					}
					require.NoError(t, db.InfoRegSet(ctx, reg, dims, resources, period))
				}
				require.NoError(t, db.InfoRegSet(ctx, collision, map[string]any{"Ключ": "collision"}, map[string]any{"Флаг": "1", "ДатаДляКалендаря": moment.UTC().Format(time.RFC3339), "Значение": "100"}, nil))
				require.NoError(t, db.InfoRegSet(ctx, unknown, map[string]any{"Ключ": "unknown"}, map[string]any{"Флаг": "1"}, nil))
				compile := func(t *testing.T, src string) query.Result {
					t.Helper()
					src = strings.ReplaceAll(src, "%s", reg.Name)
					res, err := query.Compile(src, query.CompileOpts{Entities: []*metadata.Entity{target}, InfoRegs: regs, Dialect: db.Dialect()})
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
							source := "(ВЫБРАТЬ " + modifier + projection + " ИЗ РегистрСведений.%s КАК Д) КАК X"
							t.Run(fmt.Sprintf("%s%s/first=%v", modifier, star, first), func(t *testing.T) {
								t.Run("direct bool", func(t *testing.T) {
									direct := run(t, "ВЫБРАТЬ "+modifier+projection+" ИЗ РегистрСведений.%s КАК Д УПОРЯДОЧИТЬ ПО Д.Ключ")
									require.Len(t, direct, 3)
									for _, key := range []string{"id", "posted", "deletion_mark", "_version"} {
										require.NotContains(t, direct[0], key)
									}
									for _, key := range []string{"recorder", "recorder_type", "updated_at", "владелец_id"} {
										require.Contains(t, direct[0], key)
									}
									_, hasPeriod := direct[0]["period"]
									require.Equal(t, periodic, hasPeriod)
									require.Equal(t, true, direct[0]["флаг"])
									require.Equal(t, false, direct[1]["флаг"])
									require.Nil(t, direct[2]["флаг"])
								})
								for _, nested := range []bool{false, true} {
									src := source
									if nested {
										src = "(ВЫБРАТЬ X.Флаг, X.ДатаДляКалендаря, X.Числовое, X.Строковое, X.Ключ ИЗ " + src + ") КАК X"
									}
									rows := run(t, "ВЫБРАТЬ X.Флаг, Год(X.ДатаДляКалендаря) КАК Г, Месяц(X.ДатаДляКалендаря) КАК М, День(X.ДатаДляКалендаря) КАК Д ИЗ "+src+" УПОРЯДОЧИТЬ ПО X.Ключ")
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
						rows := run(t, "ВЫБРАТЬ "+star+", Д.Активен КАК Флаг ИЗ РегистрСведений.%s КАК Д ЛЕВОЕ СОЕДИНЕНИЕ РегистрСведений.%s КАК Е ПО Е.Ключ=Д.Ключ УПОРЯДОЧИТЬ ПО Д.Ключ")
						require.Len(t, rows, 3)
						require.Equal(t, true, rows[0]["флаг"])
						require.Equal(t, false, rows[1]["флаг"])
						require.Nil(t, rows[2]["флаг"])
					})
				}
				// Expanding a derived source must include its * and explicit names across
				// another boundary, rather than treating only the latter as its full layout.
				t.Run("derived wildcard source", func(t *testing.T) {
					rows := run(t, `ВЫБРАТЬ X.*, X.ИсходныйФлаг КАК Флаг ИЗ (ВЫБРАТЬ Д.*, Д.Активен КАК ИсходныйФлаг, Д.Момент КАК ДатаДляКалендаря ИЗ РегистрСведений.%s КАК Д) КАК X УПОРЯДОЧИТЬ ПО X.Ключ`)
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
							src := "(ВЫБРАТЬ " + projection + " ИЗ РегистрСведений.%s КАК Д ЛЕВОЕ СОЕДИНЕНИЕ РегистрСведений.StarCollision КАК С ПО 1=1) КАК X"
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
					res := compile(t, `ВЫБРАТЬ X.Владелец_id КАК Флаг ИЗ (ВЫБРАТЬ Д.*, Д.Активен КАК Владелец_id ИЗ РегистрСведений.%s КАК Д) КАК X УПОРЯДОЧИТЬ ПО X.Ключ`)
					require.Empty(t, res.BoolColumns)
					rows, _, err := query.Run(ctx, db, &res)
					if db.Dialect().Name() == "postgres" {
						require.ErrorContains(t, err, "ambiguous")
						return
					}
					require.NoError(t, err, res.SQL)
					require.Len(t, rows, 3)
					for _, row := range rows {
						require.Equal(t, targetID.String(), fmt.Sprint(row["флаг"]))
					}
				})

				aliases := []string{"активен", "владелец_id", "recorder", "recorder_type", "updated_at"}
				if periodic {
					aliases = append(aliases, "period")
				}
				for _, alias := range aliases {
					for _, duplicates := range []int{1, 2, 3} {
						t.Run(fmt.Sprintf("physical key/%s/%d", alias, duplicates), func(t *testing.T) {
							inner := "ВЫБРАТЬ Д.*" + strings.Repeat(", Д.Активен КАК "+alias, duplicates) + " ИЗ РегистрСведений.%s КАК Д"
							res := compile(t, inner+" УПОРЯДОЧИТЬ ПО Д.Ключ")
							require.Empty(t, res.BoolColumns)
							rows, _, err := query.Run(ctx, db, &res)
							require.NoError(t, err, res.SQL)
							if db.Dialect().Name() == "sqlite" {
								require.Equal(t, int64(1), rows[0][alias])
								require.Equal(t, int64(0), rows[1][alias])
								require.Nil(t, rows[2][alias])
							}
							res = compile(t, "ВЫБРАТЬ X."+alias+" КАК Флаг ИЗ ("+inner+") КАК X УПОРЯДОЧИТЬ ПО X.Ключ")
							require.Empty(t, res.BoolColumns)
							rows, _, err = query.Run(ctx, db, &res)
							if db.Dialect().Name() == "postgres" {
								require.ErrorContains(t, err, "ambiguous")
							} else {
								require.NoError(t, err, res.SQL)
								require.NotEmpty(t, rows)
								_, isBool := rows[0]["флаг"].(bool)
								require.False(t, isBool)
							}
						})
					}
				}
				// A colliding wildcard must invalidate scalar exports too, even
				// when bool/date/number aliases are individually well typed.
				for _, key := range []string{"владелец_id", "recorder", "updated_at"} {
					for _, typ := range []string{"Момент", "Число"} {
						t.Run("scalar physical collision/"+key+"/"+typ, func(t *testing.T) {
							inner := "ВЫБРАТЬ Д.*, Д." + typ + " КАК " + key + " ИЗ РегистрСведений.%s КАК Д"
							expr := "Год(X." + key + ")"
							if typ == "Число" {
								expr = "MIN(X." + key + ")"
							}
							res := compile(t, "ВЫБРАТЬ "+expr+" КАК Результат ИЗ ("+inner+") КАК X")
							require.NotContains(t, res.SQL, "ob_local_datetime(x."+key+")")
							require.NotContains(t, res.SQL, "CAST(x."+key+" AS NUMERIC)")
							rows, _, err := query.Run(ctx, db, &res)
							if db.Dialect().Name() == "postgres" {
								require.ErrorContains(t, err, "ambiguous")
							} else {
								require.NoError(t, err, res.SQL)
								require.NotEmpty(t, rows)
							}
						})
					}
				}
				// These are entity-only keys: they must not be invented for
				// register wildcards and erase a genuinely unique alias.
				nonexistentKeys := []string{"posted", "deletion_mark", "_version", "Ссылка"}
				if !periodic {
					nonexistentKeys = append(nonexistentKeys, "period")
				}
				for _, key := range nonexistentKeys {
					t.Run("no phantom entity key/"+key, func(t *testing.T) {
						rows := run(t, "ВЫБРАТЬ Д.*, Д.Активен КАК "+key+" ИЗ РегистрСведений.%s КАК Д УПОРЯДОЧИТЬ ПО Д.Ключ")
						if key == "Ссылка" {
							key = "id"
						}
						require.Equal(t, true, rows[0][key])
						require.Equal(t, false, rows[1][key])
						require.Nil(t, rows[2][key])
					})
				}
				t.Run("automatic join expansion", func(t *testing.T) {
					res := compile(t, `ВЫБРАТЬ X.ОтдельныйФлаг, Год(X.ДатаОтчета) КАК Г, MIN(X.Числовое) КАК Мин ИЗ (ВЫБРАТЬ *, Д.Владелец.Строка КАК СтрокаСсылки, Д.Активен КАК ОтдельныйФлаг, Д.Момент КАК ДатаОтчета, Д.Число КАК Числовое ИЗ РегистрСведений.%s КАК Д) КАК X СГРУППИРОВАТЬ ПО X.ОтдельныйФлаг, X.ДатаОтчета`)
					require.Empty(t, res.BoolColumns)
					require.NotContains(t, res.SQL, "ob_local_datetime(x.датаотчета)")
					require.NotContains(t, res.SQL, "CAST(x.числовое AS NUMERIC)")
					rows, _, err := query.Run(ctx, db, &res)
					require.NoError(t, err, res.SQL)
					require.NotEmpty(t, rows)
					for _, row := range rows {
						if row["г"] != nil && db.Dialect().Name() == "sqlite" {
							require.Equal(t, 2026, intValue(t, row["г"]))
							_, isBool := row["отдельныйфлаг"].(bool)
							require.False(t, isBool)
						}
					}
				})

				t.Run("unknown scalar expansion", func(t *testing.T) {
					res := compile(t, `ВЫБРАТЬ X.ОтдельныйФлаг, Год(X.ДатаОтчета) КАК Г, MIN(X.Числовое) КАК Мин ИЗ (ВЫБРАТЬ С.*, Д.Активен КАК ОтдельныйФлаг, Д.Момент КАК ДатаОтчета, Д.Число КАК Числовое ИЗ РегистрСведений.%s КАК Д ЛЕВОЕ СОЕДИНЕНИЕ РегистрСведений.StarUnknown КАК С ПО 1=1) КАК X СГРУППИРОВАТЬ ПО X.ОтдельныйФлаг, X.ДатаОтчета`)
					require.Empty(t, res.BoolColumns)
					require.NotContains(t, res.SQL, "ob_local_datetime(x.датаотчета)")
					require.NotContains(t, res.SQL, "CAST(x.числовое AS NUMERIC)")
					rows, _, err := query.Run(ctx, db, &res)
					require.NoError(t, err, res.SQL)
					require.NotEmpty(t, rows)
					for _, row := range rows {
						if row["г"] != nil && db.Dialect().Name() == "sqlite" {
							require.Equal(t, 2026, intValue(t, row["г"]))
							_, isBool := row["отдельныйфлаг"].(bool)
							require.False(t, isBool)
						}
					}
				})

				if periodic {
					t.Run("virtual scalar expansion", func(t *testing.T) {
						res := compile(t, `ВЫБРАТЬ X.Флаг, Год(X.ДатаОтчета) КАК Г, MIN(X.Числовое) КАК Мин ИЗ (ВЫБРАТЬ С.*, Д.Активен КАК Флаг, Д.Момент КАК ДатаОтчета, Д.Число КАК Числовое ИЗ РегистрСведений.%s КАК Д ЛЕВОЕ СОЕДИНЕНИЕ РегистрСведений.%s.СрезПоследних() КАК С ПО С.Ключ=Д.Ключ) КАК X СГРУППИРОВАТЬ ПО X.Флаг, X.ДатаОтчета`)
						require.Empty(t, res.BoolColumns)
						require.NotContains(t, res.SQL, "ob_local_datetime(x.датаотчета)")
						require.NotContains(t, res.SQL, "CAST(x.числовое AS NUMERIC)")
						rows, _, err := query.Run(ctx, db, &res)
						require.NoError(t, err, res.SQL)
						require.NotEmpty(t, rows)
						for _, row := range rows {
							if row["г"] != nil && db.Dialect().Name() == "sqlite" {
								require.Equal(t, 2026, intValue(t, row["г"]))
								_, isBool := row["флаг"].(bool)
								require.False(t, isBool)
							}
						}
					})
				}

				for name, source := range map[string]string{
					"unnamed expression": "(ВЫБРАТЬ Д.*, 42 ИЗ РегистрСведений.%s КАК Д) КАК X",
					"union":              "(ВЫБРАТЬ Д.* ИЗ РегистрСведений.%s КАК Д ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Д.* ИЗ РегистрСведений.%s КАК Д) КАК X",
				} {
					t.Run("unknown derived layout/"+name, func(t *testing.T) {
						res := compile(t, "ВЫБРАТЬ X.*, Д.Активен КАК Флаг ИЗ РегистрСведений.%s КАК Д ЛЕВОЕ СОЕДИНЕНИЕ "+source+" ПО 1=1")
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
					res := compile(t, `ВЫБРАТЬ С.*, Д.Активен КАК Флаг ИЗ РегистрСведений.%s КАК Д ЛЕВОЕ СОЕДИНЕНИЕ РегистрСведений.StarUnknown КАК С ПО 1=1`)
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
	})
}
