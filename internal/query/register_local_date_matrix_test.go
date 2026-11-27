package query_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestRegisterLocalDateStringsMatrix(t *testing.T) {
	loc := time.FixedZone("UTC+3", 3*60*60)
	saved := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = saved })
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := t.Context()
		info := &metadata.InfoRegister{Name: "Сроки", Dimensions: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}},
			Resources: []metadata.Field{{Name: "Срок", Type: metadata.FieldTypeDate}}}
		require.NoError(t, db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{info}))
		start := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
		end := start.Add(10*time.Hour + 123456*time.Microsecond)
		for _, tc := range []struct {
			label   string
			value   any
			instant time.Time
		}{
			{"полночь", "2026-11-01", start},
			{"утро", "2026-11-01 10:00:00.123456", end},
			{"со смещением", "2026-11-01T09:00:00+02:00", end.Add(-123456 * time.Microsecond)},
			{"до", start.Add(-time.Second), start.Add(-time.Second)},
		} {
			value := tc.value
			if db.Dialect().Name() == "postgres" {
				// На PostgreSQL хранится timestamptz; старый SQLite TEXT содержит местные часы.
				value = tc.instant
			}
			require.NoError(t, db.InfoRegSet(ctx, info, map[string]any{"Ключ": tc.label}, map[string]any{"Срок": value}, nil))
		}
		for _, tc := range []struct {
			predicate string
			want      []string
		}{
			{"Срок = &Начало", []string{"полночь"}},
			{"Срок = &Конец", []string{"утро"}},
			{"Срок МЕЖДУ &Начало И &Конец", []string{"полночь", "утро", "со смещением"}},
			{"Срок <= &Начало", []string{"до", "полночь"}},
		} {
			t.Run(tc.predicate, func(t *testing.T) {
				result, err := query.Compile("ВЫБРАТЬ Ключ ИЗ РегистрСведений.Сроки ГДЕ "+tc.predicate, query.CompileOpts{
					InfoRegs: []*metadata.InfoRegister{info}, Dialect: db.Dialect(), Params: map[string]any{"Начало": start, "Конец": end},
				})
				require.NoError(t, err)
				rows, _, err := query.Run(ctx, db, &result)
				require.NoError(t, err)
				var got []string
				for _, row := range rows {
					got = append(got, row["ключ"].(string))
				}
				require.ElementsMatch(t, tc.want, got)
			})
		}
	})
}
