package query_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/stretchr/testify/require"
)

// #1881: execute the compiled query through the same result normalization as
// reports, widgets and DSL, with identical expectations for both databases.
func TestRunSystemBoolAndMinMaxMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		doc := &metadata.Entity{Name: "ТипыЗапроса", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Число", Type: metadata.FieldTypeNumber},
			{Name: "Текст", Type: metadata.FieldTypeString},
		}}
		own := &metadata.Entity{Name: "СвоиФлаги", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Проведен", Type: metadata.FieldTypeString},
			{Name: "ПометкаУдаления", Type: metadata.FieldTypeString},
			{Name: "Число", Type: metadata.FieldTypeString},
		}}
		entities := []*metadata.Entity{doc, own}
		require.NoError(t, db.Migrate(ctx, entities))
		for i, value := range []any{"100", "20", nil} {
			id := uuid.New()
			require.NoError(t, db.Upsert(ctx, doc.Name, id, map[string]any{"Номер": fmt.Sprint(i), "Число": value, "Текст": value}, doc))
			require.NoError(t, db.SetPosted(ctx, doc.Name, id, i == 0))
			require.NoError(t, db.MarkForDeletion(ctx, doc.Name, id, i == 1))
		}
		require.NoError(t, db.Upsert(ctx, own.Name, uuid.New(), map[string]any{
			"Наименование": "Свои", "Проведен": "1", "ПометкаУдаления": "0", "Число": "900",
		}, own))
		run := func(t *testing.T, src string) []map[string]any {
			t.Helper()
			res, err := query.Compile(src, query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
			require.NoError(t, err, src)
			rows, _, err := query.Run(ctx, db, &res)
			require.NoError(t, err, res.SQL)
			return rows
		}
		for _, tc := range []struct{ name, src, p, d string }{
			{"physical", "ВЫБРАТЬ posted, deletion_mark ИЗ Документ.ТипыЗапроса УПОРЯДОЧИТЬ ПО Номер", "posted", "deletion_mark"},
			{"logical", "ВЫБРАТЬ Проведен, ПометкаУдаления ИЗ Документ.ТипыЗапроса УПОРЯДОЧИТЬ ПО Номер", "posted", "deletion_mark"},
			{"qualified aliases", "ВЫБРАТЬ Д.Проведен КАК П, Д.ПометкаУдаления КАК У ИЗ Документ.ТипыЗапроса КАК Д УПОРЯДОЧИТЬ ПО Д.Номер", "п", "у"},
			{"nested filter", "ВЫБРАТЬ Проведен КАК П, ПометкаУдаления КАК У ИЗ Документ.ТипыЗапроса ГДЕ Ссылка В (ВЫБРАТЬ Ссылка ИЗ Документ.ТипыЗапроса) УПОРЯДОЧИТЬ ПО Номер", "п", "у"},
			{"derived aliases", "ВЫБРАТЬ Выборка.П КАК П, Выборка.У КАК У ИЗ (ВЫБРАТЬ Проведен КАК П, ПометкаУдаления КАК У, Номер ИЗ Документ.ТипыЗапроса) КАК Выборка УПОРЯДОЧИТЬ ПО Выборка.Номер", "п", "у"},
			{"derived logical", "ВЫБРАТЬ Выборка.Проведен, Выборка.ПометкаУдаления ИЗ (ВЫБРАТЬ Проведен, ПометкаУдаления, Номер ИЗ Документ.ТипыЗапроса) КАК Выборка УПОРЯДОЧИТЬ ПО Выборка.Номер", "posted", "deletion_mark"},
			{"join source types", "ВЫБРАТЬ Д.Проведен КАК П, Д.ПометкаУдаления КАК У ИЗ Справочник.СвоиФлаги КАК С ЛЕВОЕ СОЕДИНЕНИЕ Документ.ТипыЗапроса КАК Д ПО 1=1 УПОРЯДОЧИТЬ ПО Д.Номер", "п", "у"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				rows := run(t, tc.src)
				require.Len(t, rows, 3)
				for i, row := range rows {
					require.Equal(t, i == 0, row[tc.p])
					require.Equal(t, i == 1, row[tc.d])
				}
			})
		}
		t.Run("own names are strings", func(t *testing.T) {
			rows := run(t, `ВЫБРАТЬ С.Проведен, С.ПометкаУдаления ИЗ Справочник.СвоиФлаги КАК С`)
			require.Equal(t, "1", rows[0]["проведен"])
			require.Equal(t, "0", rows[0]["пометкаудаления"])
		})
		t.Run("SQL output collision keeps string", func(t *testing.T) {
			rows := run(t, `ВЫБРАТЬ Проведен, "1" КАК posted ИЗ Документ.ТипыЗапроса`)
			require.Equal(t, "1", rows[0]["posted"])
		})
		t.Run("null joined flags", func(t *testing.T) {
			rows := run(t, `ВЫБРАТЬ Д.Проведен КАК П, Д.ПометкаУдаления КАК У ИЗ Справочник.СвоиФлаги КАК С ЛЕВОЕ СОЕДИНЕНИЕ Документ.ТипыЗапроса КАК Д ПО 1=0`)
			require.Len(t, rows, 1)
			require.Nil(t, rows[0]["п"])
			require.Nil(t, rows[0]["у"])
		})
		for _, names := range [][2]string{{"МИН", "МАКС"}, {"МИНИМУМ", "МАКСИМУМ"}, {"MIN", "MAX"}} {
			for _, tc := range []struct{ name, arg, source string }{
				{"direct", "Число", "Документ.ТипыЗапроса"},
				{"distinct", "РАЗЛИЧНЫЕ Число", "Документ.ТипыЗапроса"},
				{"parenthesized", "(Число)", "Документ.ТипыЗапроса"},
				{"qualified", "Д.Число", "Документ.ТипыЗапроса КАК Д"},
				{"joined different type", "Д.Число", "Справочник.СвоиФлаги КАК С ЛЕВОЕ СОЕДИНЕНИЕ Документ.ТипыЗапроса КАК Д ПО 1=1"},
				{"derived", "Выборка.Значение", "(ВЫБРАТЬ Число КАК Значение ИЗ Документ.ТипыЗапроса) КАК Выборка"},
			} {
				t.Run(names[0]+" "+tc.name, func(t *testing.T) {
					rows := run(t, fmt.Sprintf("ВЫБРАТЬ %s(%s) КАК Мин, %s(%s) КАК Макс ИЗ %s", names[0], tc.arg, names[1], tc.arg, tc.source))
					require.Len(t, rows, 1)
					require.Equal(t, "20", fmt.Sprint(rows[0]["мин"]))
					require.Equal(t, "100", fmt.Sprint(rows[0]["макс"]))
				})
			}
			t.Run(names[0]+" text and null", func(t *testing.T) {
				rows := run(t, fmt.Sprintf("ВЫБРАТЬ %s(Текст) КАК Мин, %s(Текст) КАК Макс ИЗ Документ.ТипыЗапроса", names[0], names[1]))
				require.Equal(t, "100", rows[0]["мин"])
				require.Equal(t, "20", rows[0]["макс"])
				rows = run(t, fmt.Sprintf("ВЫБРАТЬ %s(Число) КАК Мин, %s(Число) КАК Макс ИЗ Документ.ТипыЗапроса ГДЕ Номер=\"2\"", names[0], names[1]))
				require.Nil(t, rows[0]["мин"])
				require.Nil(t, rows[0]["макс"])
			})
		}
		t.Run("nested select has own type", func(t *testing.T) {
			rows := run(t, `ВЫБРАТЬ (ВЫБРАТЬ МАКС(Число) ИЗ Документ.ТипыЗапроса) КАК Макс ИЗ Справочник.СвоиФлаги`)
			require.Equal(t, "100", fmt.Sprint(rows[0]["макс"]))
			rows = run(t, `ВЫБРАТЬ (ВЫБРАТЬ МИН(Число) ИЗ Справочник.СвоиФлаги) КАК Мин ИЗ Документ.ТипыЗапроса`)
			require.Equal(t, "900", rows[0]["мин"])
		})
	})
}
