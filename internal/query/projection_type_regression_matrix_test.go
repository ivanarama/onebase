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

// Execute the public compiler and runner: neither a wildcard collision nor a
// reference path may silently change the types consumed by reports and DSL.
func TestRunProjectionTypeRegressionsMatrix(t *testing.T) {
	for _, modifier := range []string{"", "ВСЕ ", "ALL "} {
		t.Run("modifier="+modifier, func(t *testing.T) {
			runProjectionTypeRegressionsMatrix(t, modifier)
		})
	}
}

func runProjectionTypeRegressionsMatrix(t *testing.T, modifier string) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		owner := &metadata.Entity{Name: "ProjectionOwner", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Активен", Type: metadata.FieldTypeBool},
			{Name: "Текст", Type: metadata.FieldTypeString},
			{Name: "Ref", Type: metadata.FieldTypeBool},
		}}
		doc := &metadata.Entity{Name: "ProjectionEdge", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Число", Type: metadata.FieldTypeNumber},
			{Name: "Текст", Type: metadata.FieldTypeString},
			{Name: "Активен", Type: metadata.FieldTypeBool},
			{Name: "Владелец", Type: metadata.FieldType("reference:ProjectionOwner"), RefEntity: owner.Name},
		}}
		entities := []*metadata.Entity{doc, owner}
		require.NoError(t, db.Migrate(ctx, entities))
		for i, value := range []string{"100", "20"} {
			id := uuid.New()
			require.NoError(t, db.Upsert(ctx, owner.Name, id, map[string]any{
				"Наименование": value, "Активен": i == 0, "Текст": value,
			}, owner))
			require.NoError(t, db.Upsert(ctx, doc.Name, uuid.New(), map[string]any{
				"Номер": fmt.Sprint(i), "Число": value, "Текст": value,
				"Активен": i != 0, "Владелец": id.String(),
			}, doc))
		}
		compile := func(t *testing.T, src string) query.Result {
			t.Helper()
			res, err := query.Compile(strings.ReplaceAll(src, "ВЫБРАТЬ ", "ВЫБРАТЬ "+modifier), query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
			require.NoError(t, err, src)
			return res
		}
		for _, projection := range []string{
			"Д.*, Д.Число КАК Текст", "*, Д.Число КАК Текст",
			"Д.Число КАК Текст, Д.*", "Д.Число КАК Текст, *",
		} {
			t.Run("wildcard number collision/"+projection, func(t *testing.T) {
				res := compile(t, "ВЫБРАТЬ МИН(X.Текст) КАК Мин, МАКС(X.Текст) КАК Макс ИЗ (ВЫБРАТЬ "+projection+" ИЗ Документ.ProjectionEdge КАК Д) КАК X")
				require.NotContains(t, strings.ToUpper(res.SQL), "CAST(", "wildcard provenance is unproven")
				rows, cols, err := query.Run(ctx, db, &res)
				if db.Dialect().Name() == "postgres" {
					require.ErrorContains(t, err, "ambiguous", res.SQL)
					return
				}
				require.NoError(t, err, res.SQL)
				require.Equal(t, []string{"мин", "макс"}, cols)
				require.Len(t, rows, 1)
				require.Equal(t, map[string]any{"мин": "100", "макс": "20"}, rows[0])
			})
		}
		for _, projection := range []string{"Д.*, Д.Активен КАК Текст", "Д.Активен КАК Текст, Д.*"} {
			t.Run("wildcard bool collision/"+projection, func(t *testing.T) {
				res := compile(t, "ВЫБРАТЬ X.Текст КАК Значение ИЗ (ВЫБРАТЬ "+projection+" ИЗ Документ.ProjectionEdge КАК Д) КАК X УПОРЯДОЧИТЬ ПО X.Номер")
				require.Empty(t, res.BoolColumns)
				rows, _, err := query.Run(ctx, db, &res)
				if db.Dialect().Name() == "postgres" {
					require.ErrorContains(t, err, "ambiguous", res.SQL)
					return
				}
				require.NoError(t, err, res.SQL)
				require.Len(t, rows, 2)
				if projection == "Д.*, Д.Активен КАК Текст" {
					require.Equal(t, "100", rows[0]["значение"])
					require.Equal(t, "20", rows[1]["значение"])
				} else {
					require.Equal(t, int64(0), rows[0]["значение"])
					require.Equal(t, int64(1), rows[1]["значение"])
				}
			})
		}
		for _, path := range []string{"Владелец.Активен", "Д.Владелец.Активен", "ProjectionEdge.Владелец.Активен"} {
			for _, alias := range []string{"Флаг", "Ссылка", "Reference", "Ref"} {
				t.Run("reference bool/"+path+"/"+alias, func(t *testing.T) {
					res := compile(t, "ВЫБРАТЬ "+path+" КАК "+alias+" ИЗ Документ.ProjectionEdge КАК Д УПОРЯДОЧИТЬ ПО Д.Номер")
					rows, cols, err := query.Run(ctx, db, &res)
					require.NoError(t, err, res.SQL)
					key := "флаг"
					if alias != "Флаг" {
						key = "id"
					}
					require.Equal(t, []string{key}, res.BoolColumns)
					require.Equal(t, []string{key}, cols)
					require.Equal(t, []map[string]any{{key: true}, {key: false}}, rows)
				})
			}
		}
		t.Run("reference output collision", func(t *testing.T) {
			res := compile(t, `ВЫБРАТЬ Д.Текст КАК Флаг, Д.Владелец.Активен КАК Флаг ИЗ Документ.ProjectionEdge КАК Д УПОРЯДОЧИТЬ ПО Д.Номер`)
			require.Empty(t, res.BoolColumns)
			_, _, err := query.Run(ctx, db, &res)
			require.NoError(t, err, res.SQL)
		})
		t.Run("reference string keeps its own type", func(t *testing.T) {
			res := compile(t, `ВЫБРАТЬ Д.Владелец.Текст КАК Активен ИЗ Документ.ProjectionEdge КАК Д УПОРЯДОЧИТЬ ПО Д.Номер`)
			require.Empty(t, res.BoolColumns)
			rows, _, err := query.Run(ctx, db, &res)
			require.NoError(t, err, res.SQL)
			require.Equal(t, []map[string]any{{"активен": "100"}, {"активен": "20"}}, rows)
		})
		t.Run("reference UUID is not a same-named bool", func(t *testing.T) {
			res := compile(t, `ВЫБРАТЬ Д.Владелец.Ref КАК Флаг ИЗ Документ.ProjectionEdge КАК Д УПОРЯДОЧИТЬ ПО Д.Номер`)
			require.Empty(t, res.BoolColumns)
			rows, _, err := query.Run(ctx, db, &res)
			require.NoError(t, err, res.SQL)
			require.Len(t, rows, 2)
			for _, row := range rows {
				_, err := uuid.Parse(fmt.Sprint(row["флаг"]))
				require.NoError(t, err, row)
			}
		})
		t.Run("explicit source shadows reference name", func(t *testing.T) {
			res := compile(t, `ВЫБРАТЬ Владелец.Текст КАК Активен ИЗ Документ.ProjectionEdge КАК Д ЛЕВОЕ СОЕДИНЕНИЕ Справочник.ProjectionOwner КАК Владелец ПО Д.Владелец.Ссылка = Владелец.Ссылка УПОРЯДОЧИТЬ ПО Д.Номер`)
			require.Empty(t, res.BoolColumns)
			rows, _, err := query.Run(ctx, db, &res)
			require.NoError(t, err, res.SQL)
			require.Equal(t, []map[string]any{{"активен": "100"}, {"активен": "20"}}, rows)
		})
	})
}
