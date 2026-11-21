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

// РАЗНОСТЬДАТ(Дата1, Дата2, Единица) — как в 1С: Дата2 − Дата1 в целых
// единицах после усечения обеих дат до начала единицы, по местному времени
// приложения. Одно тело на SQLite и PostgreSQL, через публичные Compile/Run.
// Asia/Kolkata (+05:30) ловит усечение часа по UTC вместо местного.
func TestDateDiffMatchesOneC(t *testing.T) {
	type pair struct{ from, to [6]int }
	pairs := map[string]pair{
		"Полночь":     {[6]int{2026, 11, 11, 23, 59, 59}, [6]int{2026, 11, 12, 0, 0, 1}},
		"КонецМесяца": {[6]int{2026, 1, 31, 10, 0, 0}, [6]int{2026, 3, 1, 9, 30, 15}},
		"НовыйГод":    {[6]int{2025, 12, 31, 23, 0, 0}, [6]int{2026, 1, 1, 1, 0, 0}},
		"Назад":       {[6]int{2026, 11, 12, 12, 0, 0}, [6]int{2026, 11, 12, 11, 58, 30}},
		"Полчаса":     {[6]int{2026, 6, 1, 10, 29, 0}, [6]int{2026, 6, 1, 10, 31, 0}},
	}
	for _, zone := range []string{"Europe/Moscow", "Asia/Kolkata"} {
		t.Run(zone, func(t *testing.T) {
			loc, err := time.LoadLocation(zone)
			require.NoError(t, err)
			saved := time.Local
			time.Local = loc
			t.Cleanup(func() { time.Local = saved })
			at := func(v [6]int) time.Time {
				return time.Date(v[0], time.Month(v[1]), v[2], v[3], v[4], v[5], 0, loc)
			}
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				ctx := context.Background()
				ent := &metadata.Entity{Name: "Интервалы", Kind: metadata.KindCatalog, Fields: []metadata.Field{
					{Name: "Наименование", Type: metadata.FieldTypeString},
					{Name: "Начало", Type: metadata.FieldTypeDate},
					{Name: "Окончание", Type: metadata.FieldTypeDate},
				}}
				require.NoError(t, db.Migrate(ctx, []*metadata.Entity{ent}))
				for name, p := range pairs {
					require.NoError(t, db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{
						"Наименование": name, "Начало": at(p.from), "Окончание": at(p.to),
					}, ent))
				}
				compiled, err := query.Compile(`
					ВЫБРАТЬ
						Наименование,
						РАЗНОСТЬДАТ(Начало, Окончание, СЕКУНДА) КАК Секунд,
						РАЗНОСТЬДАТ(Начало, Окончание, МИНУТА) КАК Минут,
						РАЗНОСТЬДАТ(Начало, Окончание, ЧАС) КАК Часов,
						РАЗНОСТЬДАТ(Начало, Окончание, ДЕНЬ) КАК Дней,
						РАЗНОСТЬДАТ(Начало, Окончание, МЕСЯЦ) КАК Месяцев,
						РАЗНОСТЬДАТ(Начало, Окончание, КВАРТАЛ) КАК Кварталов,
						РАЗНОСТЬДАТ(Начало, Окончание, ГОД) КАК Лет,
						РазностьДат(&Опора, Окончание, ДЕНЬ) КАК ОтОпоры,
						РАЗНОСТЬДАТ(Начало, Окончание, СЕКУНДА) / 60 КАК МинутДробно
					ИЗ Справочник.Интервалы
					ГДЕ РАЗНОСТЬДАТ(Начало, Окончание, СЕКУНДА) <> 0`, query.CompileOpts{
					Entities: []*metadata.Entity{ent},
					Dialect:  db.Dialect(),
					Params:   map[string]any{"Опора": time.Date(2026, 1, 1, 12, 0, 0, 0, loc)},
				})
				require.NoError(t, err, "компиляция")
				rows, _, err := query.Run(ctx, db, &compiled)
				require.NoError(t, err, "выполнение")
				require.Len(t, rows, len(pairs))

				day := func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, time.UTC) }
				ym := func(x time.Time) int { return x.Year()*12 + int(x.Month()) - 1 }
				for _, row := range rows {
					name := fmt.Sprint(row["наименование"])
					p, ok := pairs[name]
					require.True(t, ok, "строка %q", name)
					a, b := at(p.from), at(p.to)
					minute := func(x time.Time) time.Time {
						return time.Date(x.Year(), x.Month(), x.Day(), x.Hour(), x.Minute(), 0, 0, loc)
					}
					hour := func(x time.Time) time.Time {
						return time.Date(x.Year(), x.Month(), x.Day(), x.Hour(), 0, 0, 0, loc)
					}
					want := map[string]int{
						"секунд":    int(b.Sub(a) / time.Second),
						"минут":     int(minute(b).Sub(minute(a)) / time.Minute),
						"часов":     int(hour(b).Sub(hour(a)) / time.Hour),
						"дней":      int(day(b).Sub(day(a)) / (24 * time.Hour)),
						"месяцев":   ym(b) - ym(a),
						"кварталов": ym(b)/3 - ym(a)/3,
						"лет":       b.Year() - a.Year(),
						"отопоры":   int(day(b).Sub(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) / (24 * time.Hour)),
					}
					for col, w := range want {
						require.Equal(t, w, intValue(t, row[col]), "%s/%s: %s", name, col, col)
					}
					require.InDelta(t, b.Sub(a).Seconds()/60, floatValue(t, row["минутдробно"]), 1e-9, "%s: секунды/60", name)
				}
			})
		})
	}
}

func floatValue(t *testing.T, v any) float64 {
	t.Helper()
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	case string:
		var f float64
		_, err := fmt.Sscan(x, &f)
		require.NoError(t, err, "число %q", x)
		return f
	default:
		var f float64
		_, err := fmt.Sscan(fmt.Sprint(x), &f)
		require.NoError(t, err, "число %#v", v)
		return f
	}
}
