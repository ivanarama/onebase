package interpreter_test

import (
	"context"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// #2268: запись -> настоящий DSL-запрос -> Строка/Str и календарные функции.
// Ожидания выписаны явно: на UTC-хосте тест тоже должен ловить сдвиг суток,
// дробное смещение и обе стороны перехода DST. time.Local меняется только
// последовательно, без t.Parallel.
func TestToString_QueryDateLocalTimeMatrix(t *testing.T) {
	cases := []struct {
		name, zone, instant, text, clock string
	}{
		{"east_midnight", "Europe/Moscow", "2020-12-31T21:00:00Z", "01.01.2021 00:00:00", "0|0|0"},
		{"west_previous_day", "America/New_York", "2021-01-01T04:05:06Z", "31.12.2020 23:05:06", "23|5|6"},
		{"fractional_offset", "Asia/Kathmandu", "2020-12-31T18:20:06Z", "01.01.2021 00:05:06", "0|5|6"},
		{"spring_before", "America/New_York", "2021-03-14T06:59:59Z", "14.03.2021 01:59:59", "1|59|59"},
		{"spring_after", "America/New_York", "2021-03-14T07:00:00Z", "14.03.2021 03:00:00", "3|0|0"},
		{"fall_first", "America/New_York", "2021-11-07T05:30:00Z", "07.11.2021 01:30:00", "1|30|0"},
		{"fall_second", "America/New_York", "2021-11-07T06:30:00Z", "07.11.2021 01:30:00", "1|30|0"},
		{"utc", "UTC", "2021-01-01T00:05:06Z", "01.01.2021 00:05:06", "0|5|6"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loc, err := time.LoadLocation(tc.zone)
			require.NoError(t, err)
			previous := time.Local
			time.Local = loc
			t.Cleanup(func() { time.Local = previous })
			instant, err := time.Parse(time.RFC3339, tc.instant)
			require.NoError(t, err)
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				entity := stringDateEntity(t, db, instant.In(loc))
				const src = `Процедура Тест()
					Запрос = Новый Запрос;
					Запрос.Текст = "ВЫБРАТЬ Момент ИЗ Справочник.СтроковыеДаты";
					Д = Запрос.Выполнить()[0].Момент;
					Возврат Строка(Д) + "|" + Str(Д) + "|" + Строка(Час(Д))
						+ "|" + Строка(Минута(Д)) + "|" + Строка(Секунда(Д));
				КонецПроцедуры`
				assert.Equal(t, tc.text+"|"+tc.text+"|"+tc.clock, runOnDBEntities(t, db, []*metadata.Entity{entity}, src))
				assertStoredStringDateInstant(t, db, entity, instant)
			})
		})
	}
}

func TestToString_EmptyDateKeepsCanonicalText(t *testing.T) {
	for _, zone := range []string{"Europe/Moscow", "America/New_York", "UTC"} {
		t.Run(zone, func(t *testing.T) {
			loc, err := time.LoadLocation(zone)
			require.NoError(t, err)
			previous := time.Local
			time.Local = loc
			t.Cleanup(func() { time.Local = previous })
			for _, expression := range []string{"Дата(0)", "Дата(1, 1, 1)", "ПустаяДата"} {
				for _, alias := range []string{"Строка", "Str"} {
					src := "Функция Тест()\nВозврат " + alias + "(" + expression + ");\nКонецФункции"
					// Нулевая дата может прийти из драйвера уже в локальной зоне.
					assert.Equal(t, "01.01.0001 00:00:00", runStringConversion(t, src,
						map[string]any{"ПустаяДата": time.Time{}.In(loc)}))
				}
			}
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				entity := stringDateEntity(t, db, time.Time{})
				const src = `Процедура Тест()
					Запрос = Новый Запрос;
					Запрос.Текст = "ВЫБРАТЬ Момент ИЗ Справочник.СтроковыеДаты";
					Возврат Строка(Запрос.Выполнить()[0].Момент);
				КонецПроцедуры`
				assert.Equal(t, "01.01.0001 00:00:00", runOnDBEntities(t, db, []*metadata.Entity{entity}, src))
				assertStoredStringDateInstant(t, db, entity, time.Time{})
			})
		})
	}
}

func stringDateEntity(t *testing.T, db *storage.DB, value time.Time) *metadata.Entity {
	t.Helper()
	entity := &metadata.Entity{Name: "СтроковыеДаты", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Момент", Type: metadata.FieldTypeDate}}}
	require.NoError(t, db.Migrate(context.Background(), []*metadata.Entity{entity}))
	require.NoError(t, db.Upsert(context.Background(), entity.Name, uuid.New(), map[string]any{"Момент": value}, entity))
	return entity
}

func assertStoredStringDateInstant(t *testing.T, db *storage.DB, entity *metadata.Entity, want time.Time) {
	t.Helper()
	compiled, err := query.Compile(`ВЫБРАТЬ Момент ИЗ Справочник.СтроковыеДаты`,
		query.CompileOpts{Entities: []*metadata.Entity{entity}, Dialect: db.Dialect()})
	require.NoError(t, err)
	rows, _, err := query.Run(context.Background(), db, &compiled)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	actual, ok := rows[0]["момент"].(time.Time)
	require.True(t, ok, "тип даты из query.Run: %T", rows[0]["момент"])
	assert.True(t, actual.Equal(want), "момент сохранён: записано %v, прочитано %v", want, actual)
}
