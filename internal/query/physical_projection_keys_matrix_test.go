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

// A logical alias and its SQLIdent spelling occupy one physical output slot.
// Check real driver values/types through Compile -> Run, including the child
// SELECT where scalar rewrites happen before result normalization.
func TestPhysicalProjectionKeysMatrix(t *testing.T) {
	const long = "ДлинныйПсевдонимФлагаКоторыйСовпадаетСФизическимКороткимИменем"
	logical := strings.ToLower(long)
	short := metadata.SQLIdent(logical)
	require.Equal(t, "длинныйпсевдонимфлагакоторы_431f090b", short)
	loc, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)
	saved := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = saved })
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := &metadata.Entity{Name: "PhysicalKeys", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Текст", Type: metadata.FieldTypeString},
			{Name: "Число", Type: metadata.FieldTypeNumber},
			{Name: "Флаг", Type: metadata.FieldTypeBool},
			{Name: "Момент", Type: metadata.FieldTypeDate},
		}}
		ents := []*metadata.Entity{ent}
		require.NoError(t, db.Migrate(ctx, ents))
		moment := time.Date(2027, 1, 1, 0, 30, 0, 0, loc)
		for i, n := range []any{"100", "20", nil} {
			var flag, date any
			if i < 2 {
				flag = i == 0
				date = moment
			}
			id := uuid.New()
			require.NoError(t, db.Upsert(ctx, ent.Name, id, map[string]any{"Номер": fmt.Sprint(i), "Текст": n, "Число": n, "Флаг": flag, "Момент": date}, ent))
			require.NoError(t, db.SetPosted(ctx, ent.Name, id, i == 0))
			require.NoError(t, db.MarkForDeletion(ctx, ent.Name, id, i == 1))
		}
		compile := func(t *testing.T, src string) query.Result {
			t.Helper()
			res, err := query.Compile(src, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
			require.NoError(t, err, src)
			return res
		}
		// Direct duplicate outputs keep exactly the driver's last value, including
		// int64 (SQLite) versus int32 (PostgreSQL), native bool and SQL NULL.
		for _, field := range []string{"Проведен", "ПометкаУдаления", "Флаг"} {
			for _, reverse := range []bool{false, true} {
				for _, third := range []bool{false, true} {
					t.Run(fmt.Sprintf("direct/%s/reverse=%v/third=%v", field, reverse, third), func(t *testing.T) {
						projection := "Д." + field + " КАК " + short + ", 42 КАК " + long
						if reverse {
							projection = "42 КАК " + long + ", Д." + field + " КАК " + short
						}
						if third {
							projection += ", NULL КАК " + long
						}
						res := compile(t, "ВЫБРАТЬ "+projection+" ИЗ Документ.PhysicalKeys КАК Д УПОРЯДОЧИТЬ ПО Д.Номер")
						require.Empty(t, res.BoolColumns, res.SQL)
						raw, cols, err := db.RunQuery(ctx, res.SQL, res.Args)
						require.NoError(t, err)
						rows, gotCols, err := query.Run(ctx, db, &res)
						require.NoError(t, err)
						require.Equal(t, cols, gotCols)
						require.Equal(t, raw, rows, "values and exact Go types must remain unchanged")
						require.Len(t, rows, 3)
						if third {
							require.Nil(t, rows[0][short])
						} else if !reverse {
							if db.Dialect().Name() == "sqlite" {
								require.Equal(t, int64(42), rows[0][short])
							} else {
								require.Equal(t, int32(42), rows[0][short])
							}
						}
					})
				}
			}
		}
		for _, field := range []string{"Флаг", "Число", "Момент"} {
			for _, reverse := range []bool{false, true} {
				for _, shape := range []string{"direct", "nested", "qualified star", "bare star", "three duplicates"} {
					t.Run(fmt.Sprintf("derived/%s/reverse=%v/%s", field, reverse, shape), func(t *testing.T) {
						projection := "Д.Текст КАК " + long + ", Д." + field + " КАК " + short
						if reverse {
							projection = "Д." + field + " КАК " + short + ", Д.Текст КАК " + long
						}
						if shape == "three duplicates" {
							projection += ", Д." + field + " КАК " + long
						}
						inner := "ВЫБРАТЬ " + projection + ", Д.Номер ИЗ Документ.PhysicalKeys КАК Д"
						switch shape {
						case "nested":
							inner = "ВЫБРАТЬ Y." + short + ", Y.Номер ИЗ (" + inner + ") КАК Y"
						case "qualified star":
							inner = "ВЫБРАТЬ Y.* ИЗ (" + inner + ") КАК Y"
						case "bare star":
							inner = "ВЫБРАТЬ * ИЗ (" + inner + ") КАК Y"
						}
						src := "ВЫБРАТЬ X." + short + " КАК V ИЗ (" + inner + ") КАК X УПОРЯДОЧИТЬ ПО X.Номер"
						if field == "Число" {
							src = "ВЫБРАТЬ МИН(X." + short + ") КАК Мин, МАКС(X." + short + ") КАК Макс ИЗ (" + inner + ") КАК X"
						}
						res := compile(t, src)
						require.Empty(t, res.BoolColumns, res.SQL)
						require.Empty(t, res.DateColumns, res.SQL)
						if db.Dialect().Name() == "sqlite" {
							require.NotContains(t, res.SQL, " AS NUMERIC)", res.SQL)
						}
						rows, _, err := query.Run(ctx, db, &res)
						if db.Dialect().Name() == "postgres" {
							require.ErrorContains(t, err, "ambiguous", res.SQL)
							return
						}
						require.NoError(t, err, res.SQL)
						if field == "Число" {
							require.Len(t, rows, 1)
							require.Equal(t, "100", rows[0]["мин"])
							require.Equal(t, "20", rows[0]["макс"])
						} else {
							raw, _, err := db.RunQuery(ctx, res.SQL, res.Args)
							require.NoError(t, err)
							require.Equal(t, raw, rows, "a duplicate child key cannot lend its bool/date type")
							require.Len(t, rows, 3)
							require.Nil(t, rows[2]["v"])
						}
					})
				}
			}
		}
		// Wildcards reserve physical keys through child SELECTs, even when
		// their expressions have no proven type. Unknown unnamed outputs also
		// prevent proving that a typed alias is unique.
		for _, field := range []string{"Флаг", "Число", "Момент"} {
			for _, shape := range []string{"X.* first", "X.* last", "bare star", "unknown"} {
				t.Run("reservation/"+field+"/"+shape, func(t *testing.T) {
					projection := "Д." + field + " КАК " + long + ", X.*"
					if shape == "X.* first" {
						projection = "X.*, Д." + field + " КАК " + long
					}
					if shape == "bare star" {
						projection = "*, Д." + field + " КАК " + long
					}
					source := "Документ.PhysicalKeys КАК Д ЛЕВОЕ СОЕДИНЕНИЕ (ВЫБРАТЬ Текст КАК " + short + " ИЗ Документ.PhysicalKeys ГДЕ Номер = \"0\") КАК X ПО ИСТИНА"
					if shape == "unknown" {
						projection = "Д." + field + " КАК " + long + ", 42 + 0"
						source = "Документ.PhysicalKeys КАК Д"
					}
					inner := "ВЫБРАТЬ " + projection + " ИЗ " + source
					src := "ВЫБРАТЬ Y." + short + " КАК V ИЗ (" + inner + ") КАК Y"
					if field == "Число" {
						src = "ВЫБРАТЬ МИН(Y." + short + ") КАК V ИЗ (" + inner + ") КАК Y"
					}
					res := compile(t, src)
					require.Empty(t, res.BoolColumns, res.SQL)
					require.Empty(t, res.DateColumns, res.SQL)
					if db.Dialect().Name() == "sqlite" {
						require.NotContains(t, res.SQL, " AS NUMERIC)", res.SQL)
					}
					rows, _, err := query.Run(ctx, db, &res)
					if db.Dialect().Name() == "postgres" && shape != "unknown" {
						require.ErrorContains(t, err, "ambiguous", res.SQL)
						return
					}
					require.NoError(t, err, res.SQL)
					raw, _, err := db.RunQuery(ctx, res.SQL, res.Args)
					require.NoError(t, err)
					require.Equal(t, raw, rows)
				})
			}
		}
		// Calendar rewriting must not localize an ambiguous child output either.
		// Use equal UTC text values so both projection orders have a valid SQLite
		// calendar argument; only a proven moment is allowed to move to January 1.
		_, err = db.Exec(ctx, "UPDATE physicalkeys SET текст = "+db.Dialect().Placeholder(1), moment.UTC().Format(time.RFC3339))
		require.NoError(t, err)
		for _, reverse := range []bool{false, true} {
			projection := "Д.Текст КАК " + long + ", Д.Момент КАК " + short
			if reverse {
				projection = "Д.Момент КАК " + short + ", Д.Текст КАК " + long
			}
			t.Run(fmt.Sprintf("calendar/reverse=%v", reverse), func(t *testing.T) {
				res := compile(t, "ВЫБРАТЬ День(X."+short+") КАК День ИЗ (ВЫБРАТЬ "+projection+", Д.Номер ИЗ Документ.PhysicalKeys КАК Д) КАК X УПОРЯДОЧИТЬ ПО X.Номер")
				require.NotContains(t, res.SQL, "ob_local_datetime(")
				rows, _, err := query.Run(ctx, db, &res)
				if db.Dialect().Name() == "postgres" {
					require.ErrorContains(t, err, "ambiguous")
					return
				}
				require.NoError(t, err, res.SQL)
				require.EqualValues(t, 31, rows[0]["день"])
				require.EqualValues(t, 31, rows[1]["день"])
			})
		}
		// Unique long outputs retain the full label and proven types at every
		// boundary, whether consumers use the logical or physical spelling.
		for _, field := range []string{"Флаг", "Число", "Момент"} {
			t.Run("unique/"+field, func(t *testing.T) {
				res := compile(t, "ВЫБРАТЬ Д."+field+" КАК "+long+" ИЗ Документ.PhysicalKeys КАК Д УПОРЯДОЧИТЬ ПО Д.Номер")
				rows, cols, err := query.Run(ctx, db, &res)
				require.NoError(t, err)
				require.Equal(t, []string{logical}, cols)
				require.Len(t, rows, 3)
				require.Nil(t, rows[2][logical])
				switch field {
				case "Флаг":
					require.Equal(t, true, rows[0][logical])
					require.Equal(t, false, rows[1][logical])
				case "Момент":
					require.IsType(t, time.Time{}, rows[0][logical])
					require.True(t, moment.Equal(rows[0][logical].(time.Time)))
				case "Число":
					require.Equal(t, "100", fmt.Sprint(rows[0][logical]))
				}
				for _, name := range []string{long, short} {
					inner := "ВЫБРАТЬ Д." + field + " КАК " + long + ", Д.Номер ИЗ Документ.PhysicalKeys КАК Д"
					src := "ВЫБРАТЬ X." + name + " КАК V ИЗ (" + inner + ") КАК X УПОРЯДОЧИТЬ ПО X.Номер"
					if field == "Число" {
						src = "ВЫБРАТЬ МИН(X." + name + ") КАК Мин, МАКС(X." + name + ") КАК Макс ИЗ (" + inner + ") КАК X"
					}
					if field == "Момент" {
						src = "ВЫБРАТЬ День(X." + name + ") КАК V ИЗ (" + inner + ") КАК X УПОРЯДОЧИТЬ ПО X.Номер"
					}
					r := compile(t, src)
					got, _, err := query.Run(ctx, db, &r)
					require.NoError(t, err, r.SQL)
					switch field {
					case "Флаг":
						require.Equal(t, true, got[0]["v"])
						require.Equal(t, false, got[1]["v"])
						require.Nil(t, got[2]["v"])
					case "Число":
						require.Equal(t, "20", fmt.Sprint(got[0]["мин"]))
						require.Equal(t, "100", fmt.Sprint(got[0]["макс"]))
					case "Момент":
						require.EqualValues(t, 1, got[0]["v"])
						require.Nil(t, got[2]["v"])
					}
				}
			})
		}
	})
}
