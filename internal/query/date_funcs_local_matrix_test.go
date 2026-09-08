package query_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Календарные функции запроса обязаны видеть тот же местный день, что DSL и UI.
// Два пояса ловят оба направления перехода через UTC-границу, а январь и июль
// закрепляют зимнее/летнее смещение America/New_York. Тест идёт через публичные
// Compile и Run и одним телом проверяет SQLite и PostgreSQL.
func TestDateFunctionsUseApplicationLocalTime(t *testing.T) {
	for _, zoneName := range []string{"Asia/Kolkata", "America/New_York"} {
		zoneName := zoneName
		t.Run(zoneName, func(t *testing.T) {
			loc, err := time.LoadLocation(zoneName)
			require.NoError(t, err, "загрузка часового пояса")

			savedLocal := time.Local
			time.Local = loc
			t.Cleanup(func() { time.Local = savedLocal })

			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				ctx := context.Background()
				ent := &metadata.Entity{
					Name: "КалендарныеФункции",
					Kind: metadata.KindCatalog,
					Fields: []metadata.Field{
						{Name: "Наименование", Type: metadata.FieldTypeString},
						{Name: "Момент", Type: metadata.FieldTypeDate},
					},
				}
				require.NoError(t, db.Migrate(ctx, []*metadata.Entity{ent}), "миграция")

				moments := map[string]time.Time{
					"Зима": localBoundaryMoment(loc, zoneName, 2026, time.January),
					"Лето": localBoundaryMoment(loc, zoneName, 2026, time.July),
				}
				for name, moment := range moments {
					require.NoError(t, db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{
						"Наименование": name,
						"Момент":       moment,
					}, ent), "запись %s", name)
				}

				compiled, err := query.Compile(`
					ВЫБРАТЬ
						Наименование,
						Момент,
						Год(Момент) КАК ГодМомента,
						Месяц(Момент) КАК МесяцМомента,
						День(Момент) КАК ДеньМомента,
						НачалоДня(Момент) КАК НачалоДняМомента,
						КонецДня(Момент) КАК КонецДняМомента,
						НачалоМесяца(Момент) КАК НачалоМесяцаМомента,
						НачалоГода(Момент) КАК НачалоГодаМомента
					ИЗ Справочник.КалендарныеФункции`, query.CompileOpts{
					Entities: []*metadata.Entity{ent},
					Dialect:  db.Dialect(),
				})
				require.NoError(t, err, "компиляция")

				rows, _, err := query.Run(ctx, db, &compiled)
				require.NoError(t, err, "выполнение")
				require.Len(t, rows, len(moments), "строк результата")

				for _, row := range rows {
					name, ok := row["наименование"].(string)
					require.True(t, ok, "Наименование: %#v", row["наименование"])
					want, ok := moments[name]
					require.True(t, ok, "неожиданная строка %q", name)

					raw, ok := row["момент"].(time.Time)
					require.True(t, ok, "сырой Момент должен быть датой, получено %T", row["момент"])
					require.True(t, raw.Equal(want), "сырой Момент: %s != %s", raw, want)
					require.Equal(t, want.Year(), intValue(t, row["годмомента"]))
					require.Equal(t, int(want.Month()), intValue(t, row["месяцмомента"]))
					require.Equal(t, want.Day(), intValue(t, row["деньмомента"]))

					assertLocalClock(t, row["началоднямомента"], want.Year(), want.Month(), want.Day(), 0, 0, 0)
					assertLocalClock(t, row["конецднямомента"], want.Year(), want.Month(), want.Day(), 23, 59, 59)
					assertLocalClock(t, row["началомесяцамомента"], want.Year(), want.Month(), 1, 0, 0, 0)
					assertLocalClock(t, row["началогодамомента"], want.Year(), time.January, 1, 0, 0, 0)
				}
			})
		})
	}
}

func localBoundaryMoment(loc *time.Location, zoneName string, year int, month time.Month) time.Time {
	if zoneName == "Asia/Kolkata" {
		// В положительной зоне местная полночь хранится предыдущим UTC-днём.
		return time.Date(year, month, 15, 0, 30, 0, 0, loc)
	}
	// В отрицательной зоне поздний вечер хранится следующим UTC-днём.
	return time.Date(year, month, 15, 23, 30, 0, 0, loc)
}

func intValue(t *testing.T, value any) int {
	t.Helper()
	switch v := value.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		t.Fatalf("ожидалось число, получено %T (%#v)", value, value)
		return 0
	}
}

func assertLocalClock(t *testing.T, value any, year int, month time.Month, day, hour, minute, second int) {
	t.Helper()
	got, ok := query.ToDateValue(value)
	require.True(t, ok, "ожидалась дата, получено %T (%#v)", value, value)
	got = got.In(time.Local)
	require.Equal(t,
		fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d", year, month, day, hour, minute, second),
		got.Format("2006-01-02 15:04:05"),
	)
}
