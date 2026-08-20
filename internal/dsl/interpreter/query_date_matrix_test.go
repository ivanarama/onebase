package interpreter_test

// Заявка #1013: дата из Запрос.Выполнить() не приводилась к типу. На SQLite
// колонка даты — TEXT, поэтому из запроса приходила строка RFC3339, и сравнение
// уходило в текстовое: разделитель «T» (0x54) больше пробела (0x20), из-за чего
// запись с сегодняшней датой оказывалась «в будущем». Объектный путь
// (ПолучитьОбъект) при этом давно отдаёт значение даты — два пути чтения вели
// себя по-разному.
//
// Тесты матричные: именно здесь диалекты расходятся. На PostgreSQL драйвер и так
// отдаёт time.Time, и раздельный тест был бы зелёным при сломанном SQLite.
//
// Сам симптом (материал с сегодняшней датой «в будущем») зависит от часового
// пояса машины — на MSK текстовое сравнение случайно даёт верный ответ, — поэтому
// гейтом служат проверки типа и равенства с датой-литералом: они не зависят ни
// от пояса, ни от времени суток.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func datedCatalog() *metadata.Entity {
	return &metadata.Entity{
		Name: "Материалы",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{ID: "f_name", Name: "Наименование", Type: metadata.FieldTypeString},
			{ID: "f_pub", Name: "ДатаПубликации", Type: metadata.FieldTypeDate},
		},
	}
}

// Тип значения из запроса обязан совпадать с объектным путём — «Дата», а не
// «Строка», и над ним обязана работать арифметика дат.
func TestQueryDateColumnIsDate(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := datedCatalog()
		require.NoError(t, db.Migrate(ctx, []*metadata.Entity{ent}))
		require.NoError(t, db.Upsert(ctx, ent.Name, uuid.New(),
			map[string]any{"Наименование": "Статья", "ДатаПубликации": time.Date(2026, 3, 15, 12, 0, 0, 0, time.Local)}, ent))

		const src = `Процедура Тест()
			Запрос = Новый Запрос;
			Запрос.Текст = "ВЫБРАТЬ Наименование, ДатаПубликации КАК Д ИЗ Справочник.Материалы";
			Стр = Запрос.Выполнить()[0];
			Возврат ТипЗнч(Стр.Д) + "/" + Строка(Год(Стр.Д)) + "/" + Строка(Месяц(Стр.Д));
		КонецПроцедуры`

		assert.Equal(t, "Дата/2026/3", runOnDB(t, db, ent, src),
			"дата из запроса должна приходить значением даты, как из ПолучитьОбъект")
	})
}

// Дата из запроса обязана хронологически совпадать с тем же моментом,
// записанным литералом Дата(...). Проверка не зависит ни от часового пояса, ни
// от времени суток: со строкой вместо даты сравнение уходит в текстовое, где
// разделитель «T» больше пробела, и «меньше либо равно» становится ложью.
//
// Проверяются именно <= и >=, а не «=»: разобранная из SQLite дата приходит в
// зоне UTC, литерал — в локальной, и строгое равенство значений их различает,
// хотя момент времени один и тот же.
func TestQueryDateEqualsDateLiteral(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := datedCatalog()
		require.NoError(t, db.Migrate(ctx, []*metadata.Entity{ent}))
		require.NoError(t, db.Upsert(ctx, ent.Name, uuid.New(),
			map[string]any{"Наименование": "Статья", "ДатаПубликации": time.Date(2026, 3, 15, 12, 0, 0, 0, time.Local)}, ent))

		const src = `Процедура Тест()
			Запрос = Новый Запрос;
			Запрос.Текст = "ВЫБРАТЬ ДатаПубликации КАК Д ИЗ Справочник.Материалы";
			Стр = Запрос.Выполнить()[0];
			Эталон = Дата(2026, 3, 15, 12, 0, 0);
			Возврат Строка(Стр.Д <= Эталон) + "/" + Строка(Стр.Д >= Эталон);
		КонецПроцедуры`

		assert.Equal(t, "true/true", runOnDB(t, db, ent, src),
			"дата из запроса не совпала с тем же моментом литералом — сравнение ушло в текстовое")
	})
}

// Симптом, из-за которого заявка и появилась: материал с сегодняшней датой
// оказывался «в будущем», и страница не отдавалась. Даты хранятся локальной
// полуночью — так их и записывает конфигурация через Дата(Год, Месяц, День).
func TestQueryDateComparesChronologically(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := datedCatalog()
		require.NoError(t, db.Migrate(ctx, []*metadata.Entity{ent}))

		now := time.Now()
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
		rows := []struct {
			name string
			pub  time.Time
		}{
			{"Сегодняшний", today},
			{"Вчерашний", today.AddDate(0, 0, -1)},
			{"Завтрашний", today.AddDate(0, 0, 1)},
		}
		for _, r := range rows {
			require.NoError(t, db.Upsert(ctx, ent.Name, uuid.New(),
				map[string]any{"Наименование": r.name, "ДатаПубликации": r.pub}, ent))
		}

		const src = `Процедура Тест()
			Запрос = Новый Запрос;
			Запрос.Текст = "ВЫБРАТЬ Наименование, ДатаПубликации КАК Д ИЗ Справочник.Материалы УПОРЯДОЧИТЬ ПО Наименование";
			Отчёт = "";
			Для Каждого Стр Из Запрос.Выполнить() Цикл
				Опубликован = "нет";
				Если Стр.Д <= ТекущаяДата() Тогда
					Опубликован = "да";
				КонецЕсли;
				Отчёт = Отчёт + Стр.Наименование + ":" + Опубликован + ";";
			КонецЦикла;
			Возврат Отчёт;
		КонецПроцедуры`

		assert.Equal(t, "Вчерашний:да;Завтрашний:нет;Сегодняшний:да;", runOnDB(t, db, ent, src),
			"сравнение дат из запроса ушло в текстовое: «T» больше пробела, и сегодняшняя дата стала будущей")
	})
}
