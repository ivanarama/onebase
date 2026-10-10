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
			{"derived physical from logical", "ВЫБРАТЬ Выборка.posted, Выборка.deletion_mark ИЗ (ВЫБРАТЬ Проведен, ПометкаУдаления, Номер ИЗ Документ.ТипыЗапроса) КАК Выборка УПОРЯДОЧИТЬ ПО Выборка.Номер", "posted", "deletion_mark"},
			{"derived mixed names", "ВЫБРАТЬ Выборка.Проведен КАК П, Выборка.deletion_mark КАК У ИЗ (ВЫБРАТЬ Проведен, ПометкаУдаления, Номер ИЗ Документ.ТипыЗапроса) КАК Выборка УПОРЯДОЧИТЬ ПО Выборка.Номер", "п", "у"},
			{"derived two levels", "ВЫБРАТЬ Внешняя.posted, Внешняя.deletion_mark ИЗ (ВЫБРАТЬ Выборка.Проведен, Выборка.deletion_mark, Выборка.Номер ИЗ (ВЫБРАТЬ Проведен, ПометкаУдаления, Номер ИЗ Документ.ТипыЗапроса) КАК Выборка) КАК Внешняя УПОРЯДОЧИТЬ ПО Внешняя.Номер", "posted", "deletion_mark"},
			{"derived explicit logical aliases", "ВЫБРАТЬ Выборка.Проведен, Выборка.ПометкаУдаления ИЗ (ВЫБРАТЬ Проведен КАК Проведен, ПометкаУдаления КАК ПометкаУдаления, Номер ИЗ Документ.ТипыЗапроса) КАК Выборка УПОРЯДОЧИТЬ ПО Выборка.Номер", "проведен", "пометкаудаления"},
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
		for _, tc := range []struct {
			projection string
			ownFields  bool
			join       bool
		}{
			{"Д.Проведен, С.Проведен, Д.ПометкаУдаления, С.ПометкаУдаления", true, true},
			{"С.Проведен, Д.Проведен, С.ПометкаУдаления, Д.ПометкаУдаления", true, true},
			{"Д.Проведен, 42 КАК Проведен, Д.ПометкаУдаления, 43 КАК ПометкаУдаления", false, false},
			{"42 КАК Проведен, Д.Проведен, 43 КАК ПометкаУдаления, Д.ПометкаУдаления", false, false},
			{"Проведен, 42 КАК Проведен, ПометкаУдаления, 43 КАК ПометкаУдаления", false, false},
			{"Проведен, 42 КАК Проведен, ПометкаУдаления, 43 КАК ПометкаУдаления", false, true},
		} {
			t.Run(fmt.Sprintf("distinct SQL keys/join=%v/%s", tc.join, tc.projection), func(t *testing.T) {
				source := "Документ.ТипыЗапроса КАК Д"
				if tc.join {
					source += " ЛЕВОЕ СОЕДИНЕНИЕ Справочник.СвоиФлаги КАК С ПО 1=1"
				}
				rows := run(t, "ВЫБРАТЬ "+tc.projection+" ИЗ "+source+" УПОРЯДОЧИТЬ ПО Д.Номер")
				require.Len(t, rows, 3)
				for i, row := range rows {
					require.Equal(t, i == 0, row["posted"])
					require.Equal(t, i == 1, row["deletion_mark"])
					if tc.ownFields {
						require.Equal(t, "1", row["проведен"])
						require.Equal(t, "0", row["пометкаудаления"])
					} else {
						require.EqualValues(t, 42, row["проведен"])
						require.EqualValues(t, 43, row["пометкаудаления"])
					}
				}
			})
		}
		// A derived projection must resolve types by SQL key, including every
		// colliding expression, before either physical or logical access.
		for _, projection := range []string{
			`42 КАК posted, Проведен, 43 КАК deletion_mark, ПометкаУдаления`,
			`Проведен, 42 КАК posted, ПометкаУдаления, 43 КАК deletion_mark`,
			`42 КАК posted, Проведен, posted, 43 КАК deletion_mark, ПометкаУдаления, deletion_mark`,
			`"1" КАК posted, Проведен, "0" КАК deletion_mark, ПометкаУдаления`,
		} {
			for _, access := range []string{
				`Выборка.Проведен КАК П, Выборка.ПометкаУдаления КАК У`,
				`Выборка.posted КАК П, Выборка.deletion_mark КАК У`,
			} {
				t.Run("derived SQL collision/"+projection+"/"+access, func(t *testing.T) {
					src := "ВЫБРАТЬ " + access + " ИЗ (ВЫБРАТЬ " + projection + ", Номер ИЗ Документ.ТипыЗапроса) КАК Выборка УПОРЯДОЧИТЬ ПО Выборка.Номер"
					res, err := query.Compile(src, query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
					require.NoError(t, err)
					require.Empty(t, res.BoolColumns, res.SQL)
					rows, _, err := query.Run(ctx, db, &res)
					// PostgreSQL rejects references to duplicate derived SQL names;
					// SQLite selects the first occurrence. Neither may coerce it.
					if db.Dialect().Name() == "postgres" {
						require.ErrorContains(t, err, "ambiguous")
						return
					}
					require.NoError(t, err, res.SQL)
					require.Len(t, rows, 3)
					for i, row := range rows {
						require.IsType(t, row["п"], row["у"])
						_, isBool := row["п"].(bool)
						require.False(t, isBool, "colliding SQL key must not be normalized")
						if projection[0] == '"' {
							require.Equal(t, "1", row["п"])
							require.Equal(t, "0", row["у"])
						} else if projection[0] == '4' {
							require.EqualValues(t, 42, row["п"])
							require.EqualValues(t, 43, row["у"])
						} else {
							require.EqualValues(t, i == 0, row["п"].(int64) != 0)
							require.EqualValues(t, i == 1, row["у"].(int64) != 0)
						}
					}
				})
			}
		}
		t.Run("derived distinct SQL names keep constants", func(t *testing.T) {
			rows := run(t, `ВЫБРАТЬ Выборка.Проведен, Выборка.ПометкаУдаления, Выборка.posted, Выборка.deletion_mark ИЗ (ВЫБРАТЬ Проведен КАК Проведен, ПометкаУдаления КАК ПометкаУдаления, 42 КАК posted, 43 КАК deletion_mark, Номер ИЗ Документ.ТипыЗапроса) КАК Выборка УПОРЯДОЧИТЬ ПО Выборка.Номер`)
			for i, row := range rows {
				require.Equal(t, i == 0, row["проведен"])
				require.Equal(t, i == 1, row["пометкаудаления"])
				require.EqualValues(t, 42, row["posted"])
				require.EqualValues(t, 43, row["deletion_mark"])
			}
		})
		t.Run("derived explicit logical constants shadow flag spelling", func(t *testing.T) {
			rows := run(t, `ВЫБРАТЬ Выборка.Проведен, Выборка.ПометкаУдаления, Выборка.posted, Выборка.deletion_mark ИЗ (ВЫБРАТЬ Проведен, ПометкаУдаления, 42 КАК Проведен, 43 КАК ПометкаУдаления, Номер ИЗ Документ.ТипыЗапроса) КАК Выборка УПОРЯДОЧИТЬ ПО Выборка.Номер`)
			for i, row := range rows {
				require.EqualValues(t, 42, row["проведен"])
				require.EqualValues(t, 43, row["пометкаудаления"])
				require.Equal(t, i == 0, row["posted"])
				require.Equal(t, i == 1, row["deletion_mark"])
			}
		})
		t.Run("derived null flags", func(t *testing.T) {
			rows := run(t, `ВЫБРАТЬ Выборка.posted, Выборка.deletion_mark ИЗ (ВЫБРАТЬ Д.Проведен, Д.ПометкаУдаления ИЗ Справочник.СвоиФлаги КАК С ЛЕВОЕ СОЕДИНЕНИЕ Документ.ТипыЗапроса КАК Д ПО 1=0) КАК Выборка`)
			require.Len(t, rows, 1)
			require.Nil(t, rows[0]["posted"])
			require.Nil(t, rows[0]["deletion_mark"])
		})
		t.Run("own names are strings", func(t *testing.T) {
			rows := run(t, `ВЫБРАТЬ С.Проведен, С.ПометкаУдаления ИЗ Справочник.СвоиФлаги КАК С`)
			require.Equal(t, "1", rows[0]["проведен"])
			require.Equal(t, "0", rows[0]["пометкаудаления"])
		})
		t.Run("SQL output collision keeps string", func(t *testing.T) {
			rows := run(t, `ВЫБРАТЬ Проведен, "1" КАК posted ИЗ Документ.ТипыЗапроса`)
			require.Equal(t, "1", rows[0]["posted"])
			rows = run(t, `ВЫБРАТЬ Д.Проведен КАК Проведен, С.Проведен ИЗ Документ.ТипыЗапроса КАК Д ЛЕВОЕ СОЕДИНЕНИЕ Справочник.СвоиФлаги КАК С ПО 1=1`)
			require.Equal(t, "1", rows[0]["проведен"])
			for _, projection := range []string{`Проведен, "1" КАК posted`, `"1" КАК posted, Проведен`, `С.Проведен, Д.Проведен КАК Проведен`} {
				res, err := query.Compile("ВЫБРАТЬ "+projection+" ИЗ Документ.ТипыЗапроса КАК Д ЛЕВОЕ СОЕДИНЕНИЕ Справочник.СвоиФлаги КАК С ПО 1=1", query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
				require.NoError(t, err)
				require.Empty(t, res.BoolColumns, res.SQL)
			}
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
				{"all Russian", "ВСЕ Число", "Документ.ТипыЗапроса"},
				{"all English", "ALL Число", "Документ.ТипыЗапроса"},
				{"all qualified", "ALL Д.Число", "Документ.ТипыЗапроса КАК Д"},
				{"all derived", "ВСЕ Выборка.Значение", "(ВЫБРАТЬ Число КАК Значение ИЗ Документ.ТипыЗапроса) КАК Выборка"},
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
			for _, modifier := range []string{"", "ВСЕ ", "ALL ", "РАЗЛИЧНЫЕ "} {
				t.Run(names[0]+" text and null/"+modifier, func(t *testing.T) {
					rows := run(t, fmt.Sprintf("ВЫБРАТЬ %s(%sТекст) КАК Мин, %s(%sТекст) КАК Макс ИЗ Документ.ТипыЗапроса", names[0], modifier, names[1], modifier))
					require.Equal(t, "100", rows[0]["мин"])
					require.Equal(t, "20", rows[0]["макс"])
					rows = run(t, fmt.Sprintf("ВЫБРАТЬ %s(%sЧисло) КАК Мин, %s(%sЧисло) КАК Макс ИЗ Документ.ТипыЗапроса ГДЕ Номер=\"2\"", names[0], modifier, names[1], modifier))
					require.Nil(t, rows[0]["мин"])
					require.Nil(t, rows[0]["макс"])
				})
			}
		}
		t.Run("nested select has own type", func(t *testing.T) {
			rows := run(t, `ВЫБРАТЬ (ВЫБРАТЬ МАКС(Число) ИЗ Документ.ТипыЗапроса) КАК Макс ИЗ Справочник.СвоиФлаги`)
			require.Equal(t, "100", fmt.Sprint(rows[0]["макс"]))
			rows = run(t, `ВЫБРАТЬ (ВЫБРАТЬ МИН(Число) ИЗ Справочник.СвоиФлаги) КАК Мин ИЗ Документ.ТипыЗапроса`)
			require.Equal(t, "900", rows[0]["мин"])
		})
	})
}
