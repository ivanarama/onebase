package query_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestDerivedDateCalendarMatrix(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)
	saved := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = saved })
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := &metadata.Entity{Name: "События", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Момент", Type: metadata.FieldTypeDate},
			{Name: "Текст", Type: metadata.FieldTypeString},
		}}
		require.NoError(t, db.Migrate(ctx, []*metadata.Entity{ent}))
		if db.Dialect().Name() == "pg" {
			rows, err := db.Query(ctx, "SHOW TimeZone")
			require.NoError(t, err)
			require.True(t, rows.Next())
			var zone string
			require.NoError(t, rows.Scan(&zone))
			rows.Close()
			require.Equal(t, "Europe/Moscow", zone, "PostgreSQL session must use application timezone")
		}
		sources := []struct{ name, source, field string }{
			{"direct", "(ВЫБРАТЬ С.Момент КАК Значение ИЗ Справочник.События КАК С) КАК П", "П.Значение"},
			{"unaliased projection", "(ВЫБРАТЬ С.Момент ИЗ Справочник.События КАК С) КАК П", "П.Момент"},
			{"unqualified", "(ВЫБРАТЬ Момент КАК Значение ИЗ Справочник.События) КАК П", "Значение"},
			{"minimum", "(ВЫБРАТЬ МИНИМУМ(С.Момент) КАК Значение ИЗ Справочник.События КАК С) КАК П", "П.Значение"},
			{"maximum", "(ВЫБРАТЬ МАКСИМУМ(С.Момент) КАК Значение ИЗ Справочник.События КАК С) КАК П", "П.Значение"},
			{"join", "Справочник.События КАК С ЛЕВОЕ СОЕДИНЕНИЕ (ВЫБРАТЬ МИНИМУМ(Момент) КАК Значение ИЗ Справочник.События) КАК П ПО ИСТИНА", "П.Значение"},
			{"min shorthand", "(ВЫБРАТЬ МИН(Момент) КАК Значение ИЗ Справочник.События) КАК П", "П.Значение"},
			{"max English", "(ВЫБРАТЬ MAX(Момент) AS Значение ИЗ Справочник.События) AS П", "П.Значение"},
			{"nested", "(ВЫБРАТЬ МАКСИМУМ(П.Мин) КАК Значение ИЗ (ВЫБРАТЬ МИНИМУМ(С.Момент) КАК Мин ИЗ Справочник.События КАК С) КАК П) КАК П", "П.Значение"},
			{"parenthesized", "(ВЫБРАТЬ ((С.Момент)) КАК Значение ИЗ Справочник.События КАК С) КАК П", "П.Значение"},
		}
		// Reference aliases are emitted as id. Calendar consumers must use
		// the same key as derived projections, through every SELECT level.
		for _, alias := range []string{"Ссылка", "Reference", "Ref"} {
			for _, field := range []string{"X." + alias, alias} {
				sources = append(sources, struct{ name, source, field string }{
					"reserved/" + field,
					fmt.Sprintf("(ВЫБРАТЬ Момент КАК %s ИЗ Справочник.События) КАК X", alias), field,
				})
				sources = append(sources, struct{ name, source, field string }{
					"nested reserved/" + field,
					fmt.Sprintf("(ВЫБРАТЬ П.%s КАК %s ИЗ (ВЫБРАТЬ Момент КАК %s ИЗ Справочник.События) КАК П) КАК X", alias, alias, alias), field,
				})
			}
		}
		id := uuid.New()
		for _, moment := range []time.Time{
			time.Date(2026, time.August, 28, 0, 30, 0, 0, loc),
			time.Date(2026, time.September, 1, 0, 30, 0, 0, loc),
			time.Date(2027, time.January, 1, 0, 30, 0, 0, loc),
		} {
			require.NoError(t, db.Upsert(ctx, ent.Name, id, map[string]any{"Момент": moment, "Текст": moment.UTC().Format(time.RFC3339)}, ent))
			for _, source := range sources {
				t.Run(moment.Format("2006-01-02")+"/"+source.name, func(t *testing.T) {
					f := source.field
					text := fmt.Sprintf("ВЫБРАТЬ День(%s) КАК Д, Месяц(%s) КАК М, Год(%s) КАК Г, НачалоМесяца(%s) КАК Начало, НачалоДня(%s) КАК ДеньНачало, КонецДня(%s) КАК ДеньКонец, НачалоГода(%s) КАК ГодНачало ИЗ %s", f, f, f, f, f, f, f, source.source)
					compiled, err := query.Compile(text, query.CompileOpts{Entities: []*metadata.Entity{ent}, Dialect: db.Dialect()})
					require.NoError(t, err)
					rows, _, err := query.Run(ctx, db, &compiled)
					require.NoError(t, err, compiled.SQL)
					require.Len(t, rows, 1)
					require.Equal(t, moment.Day(), intValue(t, rows[0]["д"]), compiled.SQL)
					require.Equal(t, int(moment.Month()), intValue(t, rows[0]["м"]))
					require.Equal(t, moment.Year(), intValue(t, rows[0]["г"]))
					assertLocalClock(t, rows[0]["начало"], moment.Year(), moment.Month(), 1, 0, 0, 0)
					assertLocalClock(t, rows[0]["деньначало"], moment.Year(), moment.Month(), moment.Day(), 0, 0, 0)
					assertLocalClock(t, rows[0]["деньконец"], moment.Year(), moment.Month(), moment.Day(), 23, 59, 59)
					assertLocalClock(t, rows[0]["годначало"], moment.Year(), time.January, 1, 0, 0, 0)
				})
			}
		}
		// A date-looking string and an already localized calendar result are
		// not moments. Execute the controls on SQLite, where strings are valid
		// calendar arguments; PostgreSQL requires an explicit cast for strings.
		if db.Dialect().Name() == "sqlite" {
			for _, projection := range []string{"С.Текст", "МИНИМУМ(С.Текст)", "НачалоДня(С.Момент)"} {
				compiled, err := query.Compile(fmt.Sprintf("ВЫБРАТЬ День(П.Значение) КАК Д ИЗ (ВЫБРАТЬ %s КАК Значение ИЗ Справочник.События КАК С) КАК П", projection), query.CompileOpts{Entities: []*metadata.Entity{ent}, Dialect: db.Dialect()})
				require.NoError(t, err)
				require.NotContains(t, compiled.SQL, "ob_local_datetime(п.значение)")
				rows, _, err := query.Run(ctx, db, &compiled)
				require.NoError(t, err, compiled.SQL)
				want := 31 // stored string stays on December 31 UTC
				if strings.HasPrefix(projection, "НачалоДня") {
					want = 1 // inner calendar operation already produced January 1
				}
				require.Equal(t, want, intValue(t, rows[0]["д"]))
			}
		}
	})
}

func TestDerivedDateTypesDoNotGuess(t *testing.T) {
	dateEntity, stringEntity := dateFunctionScopeEntities()
	for _, text := range []string{
		`ВЫБРАТЬ День(П.Значение) ИЗ (ВЫБРАТЬ Т.Значение КАК Значение ИЗ Справочник.Строки КАК Т) КАК П`,
		`ВЫБРАТЬ День(П.Значение) ИЗ (ВЫБРАТЬ МИНИМУМ(Т.Значение) КАК Значение ИЗ Справочник.Строки КАК Т) КАК П`,
		`ВЫБРАТЬ День(П.Значение) ИЗ (ВЫБРАТЬ ЕстьNULL(Т.Значение, Т.Значение) КАК Значение ИЗ Справочник.Даты КАК Т) КАК П`,
		`ВЫБРАТЬ День(П.Значение) ИЗ (ВЫБРАТЬ Т.Значение КАК Значение ИЗ Справочник.Даты КАК Т ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Т.Значение ИЗ Справочник.Строки КАК Т) КАК П`,
		`ВЫБРАТЬ День(П.Значение) ИЗ (ВЫБРАТЬ Т.Значение КАК Значение, Т.Наименование КАК Значение ИЗ Справочник.Даты КАК Т) КАК П`,
		`ВЫБРАТЬ День(Значение) ИЗ Справочник.Даты КАК Д ЛЕВОЕ СОЕДИНЕНИЕ (ВЫБРАТЬ Т.Значение КАК Значение ИЗ Справочник.Строки КАК Т) КАК П ПО ИСТИНА`,
		`ВЫБРАТЬ День(Значение) ИЗ Справочник.Даты КАК Д ЛЕВОЕ СОЕДИНЕНИЕ (ВЫБРАТЬ ЕстьNULL(Т.Значение, Т.Значение) КАК Значение ИЗ Справочник.Даты КАК Т) КАК П ПО ИСТИНА`,
		`ВЫБРАТЬ День(Даты.Значение) ИЗ Справочник.Даты КАК Д ЛЕВОЕ СОЕДИНЕНИЕ (ВЫБРАТЬ Т.Значение КАК Значение ИЗ Справочник.Строки КАК Т) КАК Даты ПО ИСТИНА`,
		`ВЫБРАТЬ День(Значение) ИЗ (ВЫБРАТЬ Значение КАК Значение ИЗ Справочник.Даты ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Строки ПО ИСТИНА) КАК П`,
	} {
		compiled, err := query.Compile(text, query.CompileOpts{Entities: []*metadata.Entity{dateEntity, stringEntity}, Dialect: storage.SQLiteDialect{}})
		require.NoError(t, err)
		require.NotContains(t, compiled.SQL, "ob_local_datetime(", text)
	}
	// Identical derived aliases in sibling UNION scopes retain separate types.
	compiled, err := query.Compile(`ВЫБРАТЬ День(П.Значение) ИЗ (ВЫБРАТЬ Значение ИЗ Справочник.Даты) КАК П
		ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ День(П.Значение) ИЗ (ВЫБРАТЬ Значение ИЗ Справочник.Строки) КАК П`,
		query.CompileOpts{Entities: []*metadata.Entity{dateEntity, stringEntity}, Dialect: storage.SQLiteDialect{}})
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(compiled.SQL, "ob_local_datetime("), compiled.SQL)
}

// Undemonstrated provenance must not borrow a moment type from an alias,
// wildcard, UNION sibling, or neighboring physical source.
func TestReservedDateCalendarControlsMatrix(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)
	saved := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = saved })
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := &metadata.Entity{Name: "CalendarControls", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Момент", Type: metadata.FieldTypeDate},
			{Name: "Текст", Type: metadata.FieldTypeString},
		}}
		require.NoError(t, db.Migrate(ctx, []*metadata.Entity{ent}))
		moment := time.Date(2027, time.January, 1, 0, 30, 0, 0, loc)
		require.NoError(t, db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{
			"Момент": moment, "Текст": moment.UTC().Format(time.RFC3339),
		}, ent))
		for _, alias := range []string{"Ссылка", "Reference", "Ref"} {
			for _, field := range []string{"X." + alias, alias} {
				for _, control := range []struct {
					name, projection, pgError string
					want                      any
				}{
					{"string", "С.Текст КАК " + alias, "", 31},
					{"unknown expression", "ЕстьNULL(С.Момент, С.Момент) КАК " + alias, "", 31},
					{"null", "NULL КАК " + alias, "", nil},
					{"duplicate id", "С.Момент КАК " + alias + ", С.Момент КАК Ref", "ambiguous", 31},
					{"wildcard first", "С.*, С.Момент КАК " + alias, "ambiguous", nil},
					{"wildcard last", "С.Момент КАК " + alias + ", С.*", "ambiguous", 31},
					{"bare wildcard", "*, С.Момент КАК " + alias, "ambiguous", nil},
				} {
					t.Run(alias+"/"+field+"/"+control.name, func(t *testing.T) {
						src := fmt.Sprintf("ВЫБРАТЬ День(%s) КАК Д ИЗ (ВЫБРАТЬ %s ИЗ Справочник.CalendarControls КАК С) КАК X", field, control.projection)
						res, err := query.Compile(src, query.CompileOpts{Entities: []*metadata.Entity{ent}, Dialect: db.Dialect()})
						require.NoError(t, err)
						require.NotContains(t, res.SQL, "ob_local_datetime(", src)
						rows, _, err := query.Run(ctx, db, &res)
						if db.Dialect().Name() == "postgres" {
							if control.pgError != "" {
								require.ErrorContains(t, err, control.pgError, res.SQL)
								return
							}
							if control.name == "string" || control.name == "null" {
								require.Error(t, err, "PostgreSQL rejects untyped calendar arguments")
								return
							}
							require.NoError(t, err, res.SQL)
							require.Len(t, rows, 1)
							require.Equal(t, 1, intValue(t, rows[0]["д"]))
							return
						}
						require.NoError(t, err, res.SQL)
						require.Len(t, rows, 1)
						if control.want == nil {
							require.Nil(t, rows[0]["д"], res.SQL)
						} else {
							require.Equal(t, control.want, intValue(t, rows[0]["д"]), res.SQL)
						}
					})
				}
			}
			for _, control := range []struct{ name, source, field string }{
				{"joined string", fmt.Sprintf("Справочник.CalendarControls КАК С ЛЕВОЕ СОЕДИНЕНИЕ (ВЫБРАТЬ Текст КАК %s ИЗ Справочник.CalendarControls) КАК X ПО ИСТИНА", alias), "X." + alias},
				{"shadowed qualifier", fmt.Sprintf("Справочник.CalendarControls КАК С ЛЕВОЕ СОЕДИНЕНИЕ (ВЫБРАТЬ Текст КАК %s ИЗ Справочник.CalendarControls) КАК CalendarControls ПО ИСТИНА", alias), "CalendarControls." + alias},
				{"derived UNION", fmt.Sprintf("(ВЫБРАТЬ Момент КАК %s ИЗ Справочник.CalendarControls ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Момент КАК %s ИЗ Справочник.CalendarControls) КАК X", alias, alias), "X." + alias},
			} {
				t.Run(alias+"/"+control.name, func(t *testing.T) {
					text := fmt.Sprintf("ВЫБРАТЬ День(%s) КАК Д ИЗ %s", control.field, control.source)
					res, err := query.Compile(text, query.CompileOpts{Entities: []*metadata.Entity{ent}, Dialect: db.Dialect()})
					require.NoError(t, err)
					require.NotContains(t, res.SQL, "ob_local_datetime(")
					rows, _, err := query.Run(ctx, db, &res)
					if db.Dialect().Name() == "postgres" && control.name != "derived UNION" {
						require.Error(t, err, "string must not borrow the neighboring moment type")
						return
					}
					require.NoError(t, err, res.SQL)
					count, want := 1, 31
					if control.name == "derived UNION" {
						count = 2
					}
					if db.Dialect().Name() == "postgres" {
						want = 1
					}
					require.Len(t, rows, count)
					for _, row := range rows {
						require.Equal(t, want, intValue(t, row["д"]))
					}
				})
			}
			// The same SQL name in a different SELECT keeps its own type.
			t.Run(alias+"/UNION scopes", func(t *testing.T) {
				text := fmt.Sprintf("ВЫБРАТЬ День(X.%s) КАК Д ИЗ (ВЫБРАТЬ Момент КАК %s ИЗ Справочник.CalendarControls) КАК X ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ День(X.%s) КАК Д ИЗ (ВЫБРАТЬ ЕстьNULL(Момент, Момент) КАК %s ИЗ Справочник.CalendarControls) КАК X", alias, alias, alias, alias)
				res, err := query.Compile(text, query.CompileOpts{Entities: []*metadata.Entity{ent}, Dialect: db.Dialect()})
				require.NoError(t, err)
				rows, _, err := query.Run(ctx, db, &res)
				require.NoError(t, err, res.SQL)
				require.Len(t, rows, 2)
				require.Equal(t, 1, intValue(t, rows[0]["д"]))
				want := 31
				if db.Dialect().Name() == "postgres" {
					want = 1
				}
				require.Equal(t, want, intValue(t, rows[1]["д"]))
			})
		}
	})
}
