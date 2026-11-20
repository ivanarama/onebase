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
	"github.com/stretchr/testify/require"
)

// Parentheses preserve a direct field's SQL name. Colliding output names must
// not lend their bool/number type to another column returned by Compile → Run.
func TestRunParenthesizedOutputCollisionsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		doc := &metadata.Entity{Name: "ParenthesizedOutput", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Текст", Type: metadata.FieldTypeString},
			{Name: "Число", Type: metadata.FieldTypeNumber},
		}}
		require.NoError(t, db.Migrate(ctx, []*metadata.Entity{doc}))
		for i, value := range []string{"100", "20"} {
			id := uuid.New()
			require.NoError(t, db.Upsert(ctx, doc.Name, id, map[string]any{
				"Номер": fmt.Sprint(i), "Текст": value, "Число": value,
			}, doc))
			require.NoError(t, db.SetPosted(ctx, doc.Name, id, true))
		}
		// Use a visibly non-boolean system value, rather than the default 1.
		_, err := db.Exec(ctx, "UPDATE parenthesizedoutput SET _version = 42")
		require.NoError(t, err)
		compile := func(t *testing.T, src string) query.Result {
			t.Helper()
			res, err := query.Compile(src, query.CompileOpts{Entities: []*metadata.Entity{doc}, Dialect: db.Dialect()})
			require.NoError(t, err, src)
			return res
		}
		for _, modifier := range []string{"", "ВСЕ ", "РАЗЛИЧНЫЕ "} {
			for _, qualifier := range []string{"", "Д."} {
				for _, depth := range []int{1, 3} {
					wrap := func(field string) string {
						return strings.Repeat("(", depth) + qualifier + field + strings.Repeat(")", depth)
					}
					for _, reverse := range []bool{false, true} {
						name := fmt.Sprintf("modifier=%s/qualifier=%s/depth=%d/reverse=%v", modifier, qualifier, depth, reverse)
						t.Run("direct bool/"+name, func(t *testing.T) {
							items := []string{"Д.Проведен КАК _version", wrap("_version")}
							if reverse {
								items[0], items[1] = items[1], items[0]
							}
							res := compile(t, "ВЫБРАТЬ "+modifier+strings.Join(items, ", ")+" ИЗ Документ.ParenthesizedOutput КАК Д")
							rows, cols, err := query.Run(ctx, db, &res)
							require.NoError(t, err, res.SQL)
							require.Equal(t, []string{"_version", "_version"}, cols)
							require.NotEmpty(t, rows)
							for _, row := range rows {
								if !reverse {
									require.Equal(t, int64(42), row["_version"], res.SQL)
								} else if db.Dialect().Name() == "postgres" {
									require.Equal(t, true, row["_version"])
								} else {
									require.Equal(t, int64(1), row["_version"])
								}
							}
							require.Empty(t, res.BoolColumns, res.SQL)
						})
						t.Run("derived scalar/"+name, func(t *testing.T) {
							items := []string{wrap("Текст"), "Д.Число КАК Текст"}
							if reverse {
								items[0], items[1] = items[1], items[0]
							}
							res := compile(t, "ВЫБРАТЬ МИН(X.Текст) КАК Мин, МАКС(X.Текст) КАК Макс ИЗ (ВЫБРАТЬ "+modifier+strings.Join(items, ", ")+" ИЗ Документ.ParenthesizedOutput КАК Д) КАК X")
							rows, cols, err := query.Run(ctx, db, &res)
							if db.Dialect().Name() == "postgres" {
								require.ErrorContains(t, err, "ambiguous", res.SQL)
							} else {
								require.NoError(t, err, res.SQL)
								require.Equal(t, []string{"мин", "макс"}, cols)
								require.Equal(t, []map[string]any{{"мин": "100", "макс": "20"}}, rows, res.SQL)
							}
							require.NotContains(t, strings.ToUpper(res.SQL), "CAST(", res.SQL)
						})
					}
				}
			}
		}
		for _, field := range []string{"Проведен", "posted", "ПометкаУдаления", "deletion_mark"} {
			t.Run("physical system name/"+field, func(t *testing.T) {
				res := compile(t, "ВЫБРАТЬ Д."+field+", ((Д."+field+")) ИЗ Документ.ParenthesizedOutput КАК Д")
				require.Empty(t, res.BoolColumns, res.SQL)
				rows, cols, err := query.Run(ctx, db, &res)
				require.NoError(t, err, res.SQL)
				require.Len(t, cols, 2)
				require.Equal(t, cols[0], cols[1])
				require.Len(t, rows, 2)
			})
		}
		t.Run("derived wildcard reserves parenthesized field", func(t *testing.T) {
			res := compile(t, `ВЫБРАТЬ X.*, Д.Проведен КАК Текст ИЗ (ВЫБРАТЬ ((Текст)) ИЗ Документ.ParenthesizedOutput) КАК X ЛЕВОЕ СОЕДИНЕНИЕ Документ.ParenthesizedOutput КАК Д ПО 1=1`)
			require.Empty(t, res.BoolColumns, res.SQL)
			rows, _, err := query.Run(ctx, db, &res)
			require.NoError(t, err, res.SQL)
			require.Len(t, rows, 4)
		})
		t.Run("derived wildcard preserves separate output", func(t *testing.T) {
			res := compile(t, `ВЫБРАТЬ X.*, Д.Проведен КАК Флаг ИЗ (ВЫБРАТЬ ((Текст)) ИЗ Документ.ParenthesizedOutput) КАК X ЛЕВОЕ СОЕДИНЕНИЕ Документ.ParenthesizedOutput КАК Д ПО 1=1`)
			rows, _, err := query.Run(ctx, db, &res)
			require.NoError(t, err, res.SQL)
			require.Len(t, rows, 4)
			for _, row := range rows {
				require.Equal(t, true, row["флаг"])
				_, text := row["текст"].(string)
				require.True(t, text, row)
			}
		})
		t.Run("union keeps provenance unproven", func(t *testing.T) {
			res := compile(t, `ВЫБРАТЬ МИН(X.Текст) КАК Мин, МАКС(X.Текст) КАК Макс ИЗ (ВЫБРАТЬ ((Текст)) ИЗ Документ.ParenthesizedOutput ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Текст ИЗ Документ.ParenthesizedOutput) КАК X`)
			require.NotContains(t, strings.ToUpper(res.SQL), "CAST(")
			rows, _, err := query.Run(ctx, db, &res)
			require.NoError(t, err, res.SQL)
			require.Equal(t, []map[string]any{{"мин": "100", "макс": "20"}}, rows)
		})

		for _, projection := range []string{
			"Д.Проведен КАК Текст, ((Д.Текст))", "((Д.Текст)), Д.Проведен КАК Текст",
		} {
			t.Run("derived bool/"+projection, func(t *testing.T) {
				res := compile(t, "ВЫБРАТЬ X.Текст КАК Значение ИЗ (ВЫБРАТЬ "+projection+" ИЗ Документ.ParenthesizedOutput КАК Д) КАК X")
				require.Empty(t, res.BoolColumns, res.SQL)
				rows, _, err := query.Run(ctx, db, &res)
				if db.Dialect().Name() == "postgres" {
					require.ErrorContains(t, err, "ambiguous", res.SQL)
					return
				}
				require.NoError(t, err, res.SQL)
				require.Len(t, rows, 2)
				for _, row := range rows {
					_, isBool := row["значение"].(bool)
					require.False(t, isBool, row)
				}
			})
		}
		for _, projection := range []string{"((Д.Текст)), Д.Число КАК Значение", "Д.Число КАК Значение, ((Д.Текст))"} {
			t.Run("unique scalar/"+projection, func(t *testing.T) {
				res := compile(t, "ВЫБРАТЬ МИН(X.Значение) КАК Мин, МАКС(X.Значение) КАК Макс ИЗ (ВЫБРАТЬ "+projection+" ИЗ Документ.ParenthesizedOutput КАК Д) КАК X")
				rows, _, err := query.Run(ctx, db, &res)
				require.NoError(t, err, res.SQL)
				require.Len(t, rows, 1)
				require.Equal(t, "20", fmt.Sprint(rows[0]["мин"]))
				require.Equal(t, "100", fmt.Sprint(rows[0]["макс"]))
			})
		}
	})
}
