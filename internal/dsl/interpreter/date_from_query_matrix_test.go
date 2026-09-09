package interpreter_test

// Матричная проверка чтения даты запросом: одно и то же тело на SQLite и (при
// заданном TEST_DATABASE_URL) на PostgreSQL.
//
// Раздельными тестами этот класс не ловится: диалекты отдают ЗНАЧЕНИЕ РАЗНОГО
// ТИПА — SQLite строку RFC3339, PostgreSQL готовый time.Time из драйвера, — то
// есть исполняются разные ветки toTime. Юнит на SQLite проверяет своё ожидание,
// PG-тест своё, и оба зелёные, пока стенные часы молча расходятся. Ветка
// time.Time вдобавок недостижима на SQLite вовсе, поэтому без матрицы она
// остаётся непокрытой ровно там, где и живёт.
//
// Пояс закреплён отрицательным намеренно: на UTC-хосте (а CI гоняет тесты
// именно так) приведение к локальному тождественно и проверять нечего.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Полный круг: записали местные 17:25:37 через публичный путь записи (Upsert),
// прочитали запросом, спросили Час/День из модуля. Введённое время суток и
// календарный день обязаны вернуться без сдвига на обоих диалектах.
func TestДатаИзЗапроса_Матрица(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		закрепитьПояс(t, зона{"NY", -5 * 3600})

		ctx := context.Background()
		ent := &metadata.Entity{
			Name: "СобытиеДаты",
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Момент", Type: metadata.FieldTypeDate},
			},
		}
		require.NoError(t, db.Migrate(ctx, []*metadata.Entity{ent}), "миграция")

		// Момент так, как его строит форма: местные стенные часы (см.
		// ui.canonicalFormDate — разбор идёт через ParseInLocation в time.Local).
		момент := time.Date(2026, 7, 29, 17, 25, 37, 0, time.Local)
		require.NoError(t, db.Upsert(ctx, ent.Name, uuid.New(),
			map[string]any{"Наименование": "Звонок", "Момент": момент}, ent), "запись")

		// Календарный день: местная полночь. В поясе -05:00 она хранится уже
		// СЛЕДУЮЩИМИ сутками по UTC, и именно здесь день уезжал бы назад.
		деньБезВремени := time.Date(2026, 2, 20, 0, 0, 0, 0, time.Local)
		require.NoError(t, db.Upsert(ctx, ent.Name, uuid.New(),
			map[string]any{"Наименование": "Отгрузка", "Момент": деньБезВремени}, ent), "запись даты без времени")

		прочитано := прочитатьМомент(t, db, ent)

		assert.Equal(t, float64(17), частьДаты(t, "Час", прочитано["Звонок"]),
			"Час над датой из запроса (%T): записаны местные 17:25:37", прочитано["Звонок"])
		assert.Equal(t, float64(25), частьДаты(t, "Минута", прочитано["Звонок"]))
		assert.Equal(t, float64(29), частьДаты(t, "День", прочитано["Звонок"]))

		assert.Equal(t, float64(20), частьДаты(t, "День", прочитано["Отгрузка"]),
			"День календарной даты из запроса (%T): введено 20 февраля", прочитано["Отгрузка"])
		assert.Equal(t, float64(2), частьДаты(t, "Месяц", прочитано["Отгрузка"]))
		assert.Equal(t, float64(0), частьДаты(t, "Час", прочитано["Отгрузка"]))
	})
}

// Полный круг для Формат: в MSK записанные 30.07 01:25 хранятся как
// 29.07 22:25Z. До исправления PostgreSQL форматировал UTC-момент как 29 июля,
// а SQLite вообще печатал строку хранения целиком; День над тем же результатом
// запроса в обоих случаях уже возвращал 30.
func TestДатаИзЗапроса_ФорматМатрица(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		закрепитьПояс(t, зона{"MSK", 3 * 3600})

		ctx := context.Background()
		ent := &metadata.Entity{
			Name: "ФорматДаты",
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Момент", Type: metadata.FieldTypeDate},
			},
		}
		require.NoError(t, db.Migrate(ctx, []*metadata.Entity{ent}), "миграция")
		require.NoError(t, db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{
			"Наименование": "ГраницаСуток",
			"Момент":       time.Date(2026, 7, 30, 1, 25, 0, 0, time.Local),
		}, ent), "запись")

		прочитано := прочитатьЕдинственныйМомент(t, db, ent)
		assert.Equal(t, "30.07.2026", форматДаты(t, прочитано),
			"Формат над датой из запроса (%T)", прочитано)
		assert.Equal(t, float64(30), частьДаты(t, "День", прочитано),
			"Формат и День должны видеть один местный день")
	})
}

func прочитатьЕдинственныйМомент(t *testing.T, db *storage.DB, ent *metadata.Entity) any {
	t.Helper()
	compiled, err := query.Compile(
		`ВЫБРАТЬ Момент ИЗ Справочник.ФорматДаты`,
		query.CompileOpts{Entities: []*metadata.Entity{ent}, Dialect: db.Dialect()},
	)
	require.NoError(t, err, "компиляция запроса")

	rows, _, err := db.RunQuery(context.Background(), compiled.SQL, compiled.Args)
	require.NoError(t, err, "исполнение запроса")
	require.Len(t, rows, 1, "строк в результате")
	return rows[0]["момент"]
}

// прочитатьМомент исполняет запрос тем же компилятором, что и прикладной слой,
// и возвращает значения колонки «Момент» по наименованию — в том виде, в каком
// они доезжают до модуля.
func прочитатьМомент(t *testing.T, db *storage.DB, ent *metadata.Entity) map[string]any {
	t.Helper()
	compiled, err := query.Compile(
		`ВЫБРАТЬ Наименование, Момент ИЗ Справочник.СобытиеДаты`,
		query.CompileOpts{Entities: []*metadata.Entity{ent}, Dialect: db.Dialect()},
	)
	require.NoError(t, err, "компиляция запроса")

	rows, _, err := db.RunQuery(context.Background(), compiled.SQL, compiled.Args)
	require.NoError(t, err, "исполнение запроса")
	require.Len(t, rows, 2, "строк в результате")

	// Ключи в результате запроса — имена колонок, то есть нижний регистр
	// (обращение Стр.Наименование в модуле регистронезависимо).
	out := make(map[string]any, len(rows))
	for _, row := range rows {
		имя, _ := row["наименование"].(string)
		out[имя] = row["момент"]
	}
	require.Contains(t, out, "Звонок")
	require.Contains(t, out, "Отгрузка")
	return out
}
