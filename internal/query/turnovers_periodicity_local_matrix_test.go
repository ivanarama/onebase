package query_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Группировка виртуальной таблицы по периоду — календарная операция, и она
// обязана видеть тот же местный день, что Месяц(Период) и DSL (#1243).
// PostgreSQL после #1409 считает date_trunc в зоне приложения, а SQLite резал
// UTC-текст period: движение в 01:30 по Москве 1 октября попадало в
// сентябрьскую корзину, а в запросе с отбором «с 1 октября» появлялась строка за
// 30 сентября. Зона фиксирована именованной: в неё переход через UTC-сутки и
// месяц попадает в ночные часы.
func TestTurnoversPeriodicityUsesApplicationLocalTime(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err, "загрузка часового пояса")
	saved := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = saved })

	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		regs := matrixRegisters()
		require.NoError(t, db.MigrateRegisters(ctx, regs), "миграция регистров")
		// 01.10.2026 01:30 по Москве = 30.09.2026 22:30 UTC.
		moment := time.Date(2026, 10, 1, 1, 30, 0, 0, loc)
		require.NoError(t, db.WriteMovements(ctx, regs[0].Name, "Пост", uuid.New(),
			[]map[string]any{{"ВидДвижения": "Приход", "Номенклатура": "Стол", "Количество": float64(5)}},
			regs[0], &moment), "движение")

		run := func(text string, params map[string]any) []map[string]any {
			t.Helper()
			compiled, err := query.Compile(text, query.CompileOpts{Registers: regs, Dialect: db.Dialect(), Params: params})
			require.NoError(t, err, "компиляция")
			rows, _, err := query.Run(ctx, db, &compiled)
			require.NoError(t, err, "выполнение: %s", compiled.SQL)
			return rows
		}

		scalar := run(`ВЫБРАТЬ Месяц(Период) КАК М, День(Период) КАК Д ИЗ РегистрНакопления.ОстаткиМатрица`, nil)
		require.Len(t, scalar, 1)
		require.Equal(t, 10, intValue(t, scalar[0]["м"]), "Месяц(Период)")
		require.Equal(t, 1, intValue(t, scalar[0]["д"]), "День(Период)")

		byMonth := run(`ВЫБРАТЬ Период, КоличествоОборот ИЗ РегистрНакопления.ОстаткиМатрица.Обороты(, , Месяц)`, nil)
		require.Len(t, byMonth, 1)
		require.Equal(t, "2026-10", periodBucket(t, byMonth[0]["period"], "2006-01"), "корзина месяца")

		byDay := run(`ВЫБРАТЬ Период, КоличествоОборот ИЗ РегистрНакопления.ОстаткиМатрица.Обороты(&Нач, &Кон, День)`,
			map[string]any{
				"Нач": time.Date(2026, 10, 1, 0, 0, 0, 0, loc),
				"Кон": time.Date(2026, 10, 31, 23, 59, 59, 0, loc),
			})
		require.Len(t, byDay, 1)
		require.Equal(t, "2026-10-01", periodBucket(t, byDay[0]["period"], "2006-01-02"), "корзина дня")
	})
}

// periodBucket приводит период из группировки к строке корзины: SQLite отдаёт
// текст корзины («2026-10», «2026-10-01»), PostgreSQL — момент её начала.
func periodBucket(t *testing.T, value any, layout string) string {
	t.Helper()
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case time.Time:
		return v.In(time.Local).Format(layout)
	}
	if tm, ok := query.ToDateValue(value); ok {
		return tm.In(time.Local).Format(layout)
	}
	return fmt.Sprint(value)
}
