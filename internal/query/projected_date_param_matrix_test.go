package query_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestProjectedDateTimeParamMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := t.Context()
		field := metadata.Field{Name: "Момент", Type: metadata.FieldTypeDate}
		entity := &metadata.Entity{Name: "События", Kind: metadata.KindCatalog, Fields: []metadata.Field{field, {Name: "Ключ", Type: metadata.FieldTypeString}}}
		info := &metadata.InfoRegister{Name: "Сроки", Dimensions: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}}, Resources: []metadata.Field{field}}
		require.NoError(t, db.Migrate(ctx, []*metadata.Entity{entity}))
		require.NoError(t, db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{info}))
		at := time.Date(2026, 11, 19, 16, 0, 0, 0, time.FixedZone("UTC+3", 10800))
		for _, delta := range []time.Duration{-time.Second, 0, time.Second} {
			date := at.Add(delta)
			require.NoError(t, db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{"Момент": date.UTC().Format(time.RFC3339), "Ключ": delta.String()}, entity))
			require.NoError(t, db.InfoRegSet(ctx, info, map[string]any{"Ключ": delta.String()}, map[string]any{"Момент": date}, nil))
		}
		for _, source := range []string{"Справочник.События", "РегистрСведений.Сроки"} {
			for _, predicate := range []string{"X = &Д", "&Д = П.X", "П.X МЕЖДУ &Д И &Д"} {
				t.Run(source+"/"+predicate, func(t *testing.T) {
					sql := "ВЫБРАТЬ Ключ ИЗ (ВЫБРАТЬ Т.Ключ КАК Ключ, Т.Момент КАК X ИЗ " + source + " КАК Т) КАК П ГДЕ " + predicate
					q, err := query.Compile(sql, query.CompileOpts{Dialect: db.Dialect(), Entities: []*metadata.Entity{entity}, InfoRegs: []*metadata.InfoRegister{info}, Params: map[string]any{"Д": at}})
					require.NoError(t, err)
					rows, _, err := query.Run(ctx, db, &q)
					require.NoError(t, err, q.SQL)
					require.Len(t, rows, 1, q.SQL)
					require.Equal(t, "0s", rows[0]["ключ"], q.SQL)
				})
			}
		}
	})
}

func TestProjectedDateParamsRequireProvenType(t *testing.T) {
	entity := &metadata.Entity{Name: "Строки", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Значение", Type: metadata.FieldTypeString}}}
	for _, expr := range []string{"С.Значение", "ЕстьNULL(С.Значение, С.Значение)"} {
		q, err := query.Compile("ВЫБРАТЬ X ИЗ (ВЫБРАТЬ "+expr+" КАК X ИЗ Справочник.Строки КАК С) КАК П ГДЕ П.X = &Д", query.CompileOpts{
			Dialect: storage.SQLiteDialect{}, Entities: []*metadata.Entity{entity}, Params: map[string]any{"Д": time.Now()},
		})
		require.NoError(t, err)
		require.NotContains(t, q.SQL, "ob_utc_datetime")
	}
}
