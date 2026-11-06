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

// Exercise the public result path: restoring the full label must happen before
// bool normalization and must follow SELECT-local modifiers through wildcards.
func TestLongProjectionModifiersMatrix(t *testing.T) {
	const flag = "ИспользоватьКоэффициентИзмененияЦенМастера"
	const text = "ПодробноеОписаниеПримененияКоэффициентаИзмененияЦен"
	const alias = "ПолныйПсевдонимРезультатаКоторыйДлиннееПределаPostgreSQL"
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := &metadata.Entity{Name: "LongModifiers", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: flag, Type: metadata.FieldTypeBool},
			{Name: text, Type: metadata.FieldTypeString},
		}}
		ents := []*metadata.Entity{ent}
		require.NoError(t, db.Migrate(ctx, ents))
		flags := []any{true, false, nil, true}
		texts := []any{"Первый", "Второй", nil, "Первый"}
		for i := range flags {
			require.NoError(t, db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{
				"Номер": fmt.Sprint(i), flag: flags[i], text: texts[i],
			}, ent))
		}
		run := func(t *testing.T, src string) ([]map[string]any, []string) {
			t.Helper()
			res, err := query.Compile(src, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
			require.NoError(t, err, src)
			rows, cols, err := query.Run(ctx, db, &res)
			require.NoError(t, err, res.SQL)
			return rows, cols
		}
		for _, field := range []string{flag, text} {
			values := flags
			if field == text {
				values = texts
			}
			for _, modifier := range []string{"", "ALL ", "DISTINCT ", "ВСЕ ", "РАЗЛИЧНЫЕ "} {
				inner := "ВЫБРАТЬ " + modifier + "Д." + field + ", Д.Номер ИЗ Документ.LongModifiers КАК Д"
				plain := "ВЫБРАТЬ Д." + field + ", Д.Номер ИЗ Документ.LongModifiers КАК Д"
				for _, tc := range []struct {
					name, src, label string
					cols             []string
				}{
					{"first", inner + " УПОРЯДОЧИТЬ ПО Д.Номер", field, []string{strings.ToLower(field), "номер"}},
					{"second", "ВЫБРАТЬ " + modifier + "Д.Номер, Д." + field + " ИЗ Документ.LongModifiers КАК Д УПОРЯДОЧИТЬ ПО Д.Номер", field, []string{"номер", strings.ToLower(field)}},
					{"alias", "ВЫБРАТЬ " + modifier + "Д." + field + " КАК " + alias + ", Д.Номер ИЗ Документ.LongModifiers КАК Д УПОРЯДОЧИТЬ ПО Д.Номер", alias, []string{strings.ToLower(alias), "номер"}},
					{"qualified star", "ВЫБРАТЬ " + modifier + "Д.* ИЗ Документ.LongModifiers КАК Д УПОРЯДОЧИТЬ ПО Д.Номер", field, nil},
					{"bare star", "ВЫБРАТЬ " + modifier + "* ИЗ Документ.LongModifiers КАК Д УПОРЯДОЧИТЬ ПО Д.Номер", field, nil},
					{"child field", "ВЫБРАТЬ X." + field + ", X.Номер ИЗ (" + inner + ") КАК X УПОРЯДОЧИТЬ ПО X.Номер", field, []string{strings.ToLower(field), "номер"}},
					{"child qualified star", "ВЫБРАТЬ X.* ИЗ (" + inner + ") КАК X УПОРЯДОЧИТЬ ПО X.Номер", field, []string{strings.ToLower(field), "номер"}},
					{"child bare star", "ВЫБРАТЬ * ИЗ (" + inner + ") КАК X УПОРЯДОЧИТЬ ПО X.Номер", field, []string{strings.ToLower(field), "номер"}},
					{"outer star", "ВЫБРАТЬ " + modifier + "X.* ИЗ (" + plain + ") КАК X УПОРЯДОЧИТЬ ПО X.Номер", field, []string{strings.ToLower(field), "номер"}},
					{"both stars", "ВЫБРАТЬ " + modifier + "X.* ИЗ (" + inner + ") КАК X УПОРЯДОЧИТЬ ПО X.Номер", field, []string{strings.ToLower(field), "номер"}},
					{"nested stars", "ВЫБРАТЬ " + modifier + "Y.* ИЗ (ВЫБРАТЬ " + modifier + "X.* ИЗ (" + inner + ") КАК X) КАК Y УПОРЯДОЧИТЬ ПО Y.Номер", field, []string{strings.ToLower(field), "номер"}},
				} {
					t.Run(field+"/"+strings.TrimSpace(modifier)+"/"+tc.name, func(t *testing.T) {
						rows, cols := run(t, tc.src)
						label := strings.ToLower(tc.label)
						if tc.cols != nil {
							require.Equal(t, tc.cols, cols)
						}
						require.Contains(t, cols, label)
						require.NotContains(t, cols, metadata.SQLIdent(label))
						require.Len(t, rows, len(values))
						for i, row := range rows {
							value, exists := row[label]
							require.True(t, exists, "full result key missing: %v", row)
							want := values[i]
							// Wildcards retain the existing driver bool type; an
							// explicitly selected bool is normalized by query.Run.
							if field == flag && db.Dialect().Name() == "sqlite" && strings.Contains(tc.name, "star") && want != nil {
								want = int64(0)
								if values[i].(bool) {
									want = int64(1)
								}
							}
							require.Equal(t, want, value, "row %d must retain the exact Go type", i)
							require.NotContains(t, row, metadata.SQLIdent(label))
						}
					})
				}
				// DISTINCT still removes duplicates; ALL still retains them. This
				// also checks full labels when no second column can hide the bug.
				t.Run(field+"/"+strings.TrimSpace(modifier)+"/duplicates", func(t *testing.T) {
					rows, cols := run(t, "ВЫБРАТЬ "+modifier+"Д."+field+" ИЗ Документ.LongModifiers КАК Д")
					label := strings.ToLower(field)
					require.Equal(t, []string{label}, cols)
					want := []map[string]any{}
					selected := values
					if modifier == "DISTINCT " || modifier == "РАЗЛИЧНЫЕ " {
						selected = values[:3]
					}
					for _, value := range selected {
						want = append(want, map[string]any{label: value})
					}
					require.ElementsMatch(t, want, rows)
				})
			}
			// An aggregate's DISTINCT belongs to its argument, not the SELECT.
			t.Run(field+"/aggregate distinct", func(t *testing.T) {
				rows, cols := run(t, "ВЫБРАТЬ КОЛИЧЕСТВО(РАЗЛИЧНЫЕ Д."+field+") КАК Итог ИЗ Документ.LongModifiers КАК Д")
				require.Equal(t, []string{"итог"}, cols)
				require.Len(t, rows, 1)
				require.EqualValues(t, 2, rows[0]["итог"])
			})
		}
	})
}
