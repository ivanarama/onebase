package query_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// SELECT modifiers affect multiplicity, not the provenance of the first field.
// Exercise the compiler and runner together, including nested SELECT scopes.
func TestRunSelectModifiersMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		doc := &metadata.Entity{Name: "SelectModifier", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Число", Type: metadata.FieldTypeNumber},
			{Name: "Текст", Type: metadata.FieldTypeString},
		}}
		entities := []*metadata.Entity{doc}
		require.NoError(t, db.Migrate(ctx, entities))
		for i, value := range []any{"100", "20", "20", nil} {
			id := uuid.New()
			require.NoError(t, db.Upsert(ctx, doc.Name, id, map[string]any{
				"Номер": fmt.Sprint(i), "Число": value, "Текст": value,
			}, doc))
			require.NoError(t, db.SetPosted(ctx, doc.Name, id, i == 0))
			require.NoError(t, db.MarkForDeletion(ctx, doc.Name, id, i != 0))
		}
		run := func(t *testing.T, src string) ([]map[string]any, []string) {
			t.Helper()
			res, err := query.Compile(src, query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
			require.NoError(t, err, src)
			rows, cols, err := query.Run(ctx, db, &res)
			require.NoError(t, err, res.SQL)
			return rows, cols
		}
		for _, selectSQL := range []string{"ВЫБРАТЬ", "ВЫБРАТЬ ВСЕ", "SELECT ALL", "ВЫБРАТЬ РАЗЛИЧНЫЕ", "SELECT DISTINCT"} {
			t.Run(selectSQL, func(t *testing.T) {
				for _, fields := range [][2]string{
					{"Проведен", "ПометкаУдаления"}, {"ПометкаУдаления", "Проведен"},
					{"posted", "deletion_mark"}, {"deletion_mark", "posted"},
				} {
					for _, qualified := range []bool{false, true} {
						for _, aliases := range []bool{false, true} {
							t.Run(fmt.Sprintf("flags/%s/qualified=%v/aliases=%v", fields[0], qualified, aliases), func(t *testing.T) {
								p, d := fields[0], fields[1]
								keys := []string{"posted", "deletion_mark"}
								if fields[0] == "ПометкаУдаления" || fields[0] == "deletion_mark" {
									keys[0], keys[1] = keys[1], keys[0]
								}
								if qualified {
									p, d = "Д."+p, "Д."+d
								}
								if aliases {
									p, d = p+" КАК Первый", d+" КАК Второй"
									keys = []string{"первый", "второй"}
								}
								for _, null := range []bool{false, true} {
									source := "Документ.SelectModifier КАК Д"
									if null {
										source = "Документ.SelectModifier КАК С ЛЕВОЕ СОЕДИНЕНИЕ " + source + " ПО 1=0"
										if !qualified {
											continue // unqualified fields would belong to С
										}
									}
									inner := selectSQL + " " + p + ", " + d + " ИЗ " + source
									for _, derived := range []bool{false, true} {
										text := inner
										if derived {
											text = selectSQL + " X." + keys[0] + ", X." + keys[1] + " ИЗ (" + inner + ") КАК X"
										}
										rows, cols := run(t, text)
										require.Equal(t, keys, cols)
										wantLen := 4
										if strings.Contains(selectSQL, "DISTINCT") || strings.Contains(selectSQL, "РАЗЛИЧНЫЕ") {
											wantLen = 2
											if null {
												wantLen = 1
											}
										}
										require.Len(t, rows, wantLen)
										seen := map[bool]bool{}
										for _, row := range rows {
											if null {
												require.Nil(t, row[keys[0]])
												require.Nil(t, row[keys[1]])
												continue
											}
											require.IsType(t, true, row[keys[0]])
											require.IsType(t, false, row[keys[1]])
											require.Equal(t, !row[keys[0]].(bool), row[keys[1]])
											seen[row[keys[0]].(bool)] = true
										}
										if !null {
											require.Len(t, seen, 2)
										}
									}
								}
							})
						}
					}
				}
				for _, field := range []string{"Число", "Текст"} {
					for _, depth := range []int{1, 2} {
						for _, names := range [][2]string{{"MIN", "MAX"}, {"МИН", "МАКС"}, {"МИНИМУМ", "МАКСИМУМ"}} {
							t.Run(fmt.Sprintf("aggregate/%s/depth=%d/%s", field, depth, names[0]), func(t *testing.T) {
								for _, onlyNull := range []bool{false, true} {
									inner := selectSQL + " Д." + field + " КАК Значение ИЗ Документ.SelectModifier КАК Д"
									if onlyNull {
										inner += ` ГДЕ Д.Номер="3"`
									}
									if depth == 2 {
										inner = selectSQL + " X.Значение КАК Значение ИЗ (" + inner + ") КАК X"
									}
									rows, cols := run(t, fmt.Sprintf("%s %s(X.Значение) КАК Мин, %s(X.Значение) КАК Макс ИЗ (%s) КАК X", selectSQL, names[0], names[1], inner))
									require.Equal(t, []string{"мин", "макс"}, cols)
									require.Len(t, rows, 1)
									if onlyNull {
										require.Nil(t, rows[0]["мин"])
										require.Nil(t, rows[0]["макс"])
									} else if field == "Текст" {
										require.Equal(t, "100", rows[0]["мин"])
										require.Equal(t, "20", rows[0]["макс"])
									} else {
										for _, key := range cols {
											switch value := rows[0][key].(type) {
											case int64, float64, decimal.Decimal:
											default:
												t.Fatalf("%s must be numeric, got %T(%v)", key, value, value)
											}
										}
										require.Equal(t, "20", fmt.Sprint(rows[0]["мин"]))
										require.Equal(t, "100", fmt.Sprint(rows[0]["макс"]))
									}
								}
							})
						}
					}
				}
			})
		}
		for _, prefix := range []string{"ВЫБРАТЬ ВСЕ", "SELECT ALL"} {
			t.Run(prefix+"/scope controls", func(t *testing.T) {
				// Unknown/string outputs of X cannot borrow the number type
				// from a neighboring source, a shadowed qualifier or a UNION.
				for _, source := range []string{
					`Документ.SelectModifier КАК Д ЛЕВОЕ СОЕДИНЕНИЕ (ВЫБРАТЬ ЕстьNULL(Текст, Текст) КАК Число ИЗ Документ.SelectModifier) КАК X ПО 1=1`,
					`Документ.SelectModifier КАК Д ЛЕВОЕ СОЕДИНЕНИЕ (ВЫБРАТЬ Текст КАК Число ИЗ Документ.SelectModifier) КАК SelectModifier ПО 1=1`,
					`(ВЫБРАТЬ Текст КАК Число ИЗ Документ.SelectModifier ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Текст КАК Число ИЗ Документ.SelectModifier) КАК X`,
				} {
					qualifier := "X"
					if strings.Contains(source, ") КАК SelectModifier") {
						qualifier = "SelectModifier"
					}
					src := fmt.Sprintf("ВЫБРАТЬ МИН(%s.Число) КАК Мин, МАКС(%s.Число) КАК Макс ИЗ %s", qualifier, qualifier, source)
					src = strings.ReplaceAll(src, "ВЫБРАТЬ", prefix)
					res, err := query.Compile(src, query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
					require.NoError(t, err, src)
					require.NotContains(t, strings.ToUpper(res.SQL), "CAST(", res.SQL)
					rows, _, err := query.Run(ctx, db, &res)
					require.NoError(t, err, res.SQL)
					require.Equal(t, []map[string]any{{"мин": "100", "макс": "20"}}, rows)
				}
				src := prefix + ` X.posted КАК AllValue ИЗ Документ.SelectModifier КАК Д ЛЕВОЕ СОЕДИНЕНИЕ (` + prefix + ` ЕстьNULL(Текст, "ALL") КАК posted ИЗ Документ.SelectModifier) КАК X ПО 1=1`
				res, err := query.Compile(src, query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
				require.NoError(t, err)
				require.Empty(t, res.BoolColumns, res.SQL)
				rows, cols, err := query.Run(ctx, db, &res)
				require.NoError(t, err, res.SQL)
				require.Equal(t, []string{"allvalue"}, cols)
				require.Len(t, rows, 16)
				for _, row := range rows {
					require.IsType(t, "", row["allvalue"])
				}
				// The SELECT modifier also preserves SQL-side row restrictions.
				res, err = query.Compile(prefix+` Проведен КАК Флаг, Номер ИЗ Документ.SelectModifier ГДЕ Номер="0" ИЛИ Номер="1"`, query.CompileOpts{
					Entities: entities, Dialect: db.Dialect(),
					RowFilters: map[query.SourceRef]*storage.Predicate{
						{Kind: "document", Name: doc.Name}: {Field: "Номер", Op: "eq", Value: "0"},
					},
				})
				require.NoError(t, err)
				rows, _, err = query.Run(ctx, db, &res)
				require.NoError(t, err, res.SQL)
				require.Equal(t, []map[string]any{{"флаг": true, "номер": "0"}}, rows)
			})
		}
	})
}
