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

// Exercise result normalization and derived provenance through the public API.
// PostgreSQL names an unaliased COUNT "count", unlike SQLite's expression name.
func TestRunUnaliasedAggregateOutputCollisionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		doc := &metadata.Entity{Name: "Review2034AggregateProbe", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Число", Type: metadata.FieldTypeNumber},
		}}
		entities := []*metadata.Entity{doc}
		require.NoError(t, db.Migrate(ctx, entities))
		for i, value := range []string{"100", "20"} {
			id := uuid.New()
			require.NoError(t, db.Upsert(ctx, doc.Name, id, map[string]any{"Номер": fmt.Sprint(i), "Число": value}, doc))
			require.NoError(t, db.SetPosted(ctx, doc.Name, id, true))
		}
		compile := func(t *testing.T, text string) query.Result {
			t.Helper()
			r, err := query.Compile(text, query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
			require.NoError(t, err, text)
			return r
		}
		for _, agg := range []string{"КОЛИЧЕСТВО", "COUNT"} {
			for _, wrapped := range []bool{false, true} {
				for _, reversed := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/wrapped=%v/reversed=%v", agg, wrapped, reversed), func(t *testing.T) {
						expr := agg + "(Д.Число)"
						if wrapped {
							expr = "(" + expr + ")"
						}
						projection, aggregateIndex := "Д.Проведен КАК count, "+expr, 1
						if reversed {
							projection, aggregateIndex = expr+", Д.Проведен КАК count", 0
						}
						text := "ВЫБРАТЬ " + projection + " ИЗ Документ.Review2034AggregateProbe КАК Д СГРУППИРОВАТЬ ПО Д.Проведен"
						r := compile(t, text)
						rows, cols, err := query.Run(ctx, db, &r)
						require.NoError(t, err, r.SQL)
						require.Len(t, rows, 1)
						if db.Dialect().Name() == "postgres" {
							require.Equal(t, []string{"count", "count"}, cols)
							require.Empty(t, r.BoolColumns, "a duplicate SQL key has no proven bool type")
							if !reversed {
								require.Equal(t, int64(2), rows[0]["count"])
							}
						} else {
							require.Equal(t, []string{"count"}, r.BoolColumns)
							require.Equal(t, true, rows[0]["count"])
							require.Equal(t, int64(2), rows[0][cols[aggregateIndex]])
						}
						// A derived boundary must not export the colliding bool.
						r = compile(t, "ВЫБРАТЬ X.count КАК Флаг ИЗ ("+text+") КАК X")
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
		// Distinct names preserve the bool across multiple derived boundaries.
		for _, projection := range []string{"Д.Проведен КАК Флаг, COUNT(Д.Число)", "COUNT(Д.Число), Д.Проведен КАК Флаг"} {
			t.Run("independent/"+projection, func(t *testing.T) {
				text := "ВЫБРАТЬ " + projection + " ИЗ Документ.Review2034AggregateProbe КАК Д СГРУППИРОВАТЬ ПО Д.Проведен"
				r := compile(t, "ВЫБРАТЬ Y.Флаг ИЗ (ВЫБРАТЬ X.Флаг ИЗ ("+text+") КАК X) КАК Y")
				rows, _, err := query.Run(ctx, db, &r)
				require.NoError(t, err, r.SQL)
				require.Equal(t, true, rows[0]["флаг"])
			})
		}
		// Every aggregate synonym uses the name emitted by the translator.
		for _, tc := range []struct{ function, key string }{
			{"СУММА", "sum"}, {"SUM", "sum"}, {"СРЕДНЕЕ", "avg"}, {"AVG", "avg"},
			{"МИН", "min"}, {"МИНИМУМ", "min"}, {"MIN", "min"},
			{"МАКС", "max"}, {"МАКСИМУМ", "max"}, {"MAX", "max"},
		} {
			t.Run("aggregate/"+tc.function, func(t *testing.T) {
				r := compile(t, "ВЫБРАТЬ Д.Проведен КАК "+tc.key+", "+tc.function+"(Д.Число) ИЗ Документ.Review2034AggregateProbe КАК Д СГРУППИРОВАТЬ ПО Д.Проведен")
				rows, cols, err := query.Run(ctx, db, &r)
				require.NoError(t, err, r.SQL)
				require.Len(t, rows, 1)
				if db.Dialect().Name() == "postgres" {
					require.Equal(t, []string{tc.key, tc.key}, cols)
					require.Empty(t, r.BoolColumns)
					_, isBool := rows[0][tc.key].(bool)
					require.False(t, isBool)
				} else {
					require.Equal(t, true, rows[0][tc.key])
				}
			})
		}
		t.Run("derived wildcard collision", func(t *testing.T) {
			r := compile(t, "ВЫБРАТЬ X.*, Д.Проведен КАК count ИЗ (ВЫБРАТЬ COUNT(Число) ИЗ Документ.Review2034AggregateProbe) КАК X ЛЕВОЕ СОЕДИНЕНИЕ Документ.Review2034AggregateProbe КАК Д ПО ИСТИНА")
			rows, _, err := query.Run(ctx, db, &r)
			require.NoError(t, err, r.SQL)
			require.Len(t, rows, 2)
			require.Empty(t, r.BoolColumns, "a wildcard must reserve the child's implicit name")
		})
		// An unaliased aggregate also reserves its name against scalar exports.
		for _, projection := range []string{"Д.Число КАК count, COUNT(Д.Число)", "COUNT(Д.Число), Д.Число КАК count"} {
			t.Run("scalar/"+projection, func(t *testing.T) {
				r := compile(t, "ВЫБРАТЬ МИН(X.count) КАК Мин ИЗ (ВЫБРАТЬ "+projection+" ИЗ Документ.Review2034AggregateProbe КАК Д СГРУППИРОВАТЬ ПО Д.Число) КАК X")
				rows, _, err := query.Run(ctx, db, &r)
				if db.Dialect().Name() == "postgres" {
					require.ErrorContains(t, err, "ambiguous", r.SQL)
				} else {
					require.NoError(t, err, r.SQL)
					require.Contains(t, strings.ToUpper(r.SQL), "CAST(")
					require.EqualValues(t, 20, rows[0]["мин"])
				}
			})
		}
	})
}
