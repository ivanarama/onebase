package interpreter_test

// Цепочка `Запрос.Выполнить().Выгрузить()` — канонический способ 1С получить
// коллекцию строк, и переносимый код пишут именно так. В OneBase `Выполнить()`
// сразу отдаёт массив, поэтому привычный вызов падал в рантайме: «Метод
// выгрузить не существует у значения типа Массив». Та же цепочка при этом
// опубликована в документации платформы (`docs/ai-assistant.md`), то есть
// пример из руководства не исполнялся (issue #1364).
//
// Проверяем через тот же путь, которым код запускает пользователь: DSL-исходник
// с настоящим запросом к базе, а не вызовом метода массива напрямую.
//
// Вариант 1 из разбора триажа: совместимый метод у массива.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func unloadCatalog() *metadata.Entity {
	return &metadata.Entity{
		Name: "Номенклатура",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{ID: "f_name", Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
}

func TestQueryUnloadMatchesOneCIdiom(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := unloadCatalog()
		require.NoError(t, db.Migrate(ctx, []*metadata.Entity{ent}))
		for _, name := range []string{"Гайка", "Болт"} {
			require.NoError(t, db.Upsert(ctx, ent.Name, uuid.New(),
				map[string]any{"Наименование": name}, ent))
		}

		// Ровно та цепочка, что в заявке и в руководстве.
		const src = `Процедура Тест()
			Запрос = Новый Запрос;
			Запрос.Текст = "ВЫБРАТЬ Наименование ИЗ Справочник.Номенклатура";
			Данные = Запрос.Выполнить().Выгрузить();
			Возврат ТипЗнч(Данные) + "/" + Строка(Данные.Количество());
		КонецПроцедуры`

		assert.Equal(t, "Массив/2", runOnDB(t, db, ent, src),
			"канонический вызов 1С обязан отдавать те же строки, что Выполнить()")
	})
}

// Выгруженная коллекция — копия: в 1С Выгрузить() выгружает данные в новую
// коллекцию, и код, который её потом чистит или дополняет, не ждёт, что
// изменится исходный результат запроса.
func TestQueryUnloadReturnsIndependentCollection(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := unloadCatalog()
		require.NoError(t, db.Migrate(ctx, []*metadata.Entity{ent}))
		require.NoError(t, db.Upsert(ctx, ent.Name, uuid.New(),
			map[string]any{"Наименование": "Гайка"}, ent))

		const src = `Процедура Тест()
			Запрос = Новый Запрос;
			Запрос.Текст = "ВЫБРАТЬ Наименование ИЗ Справочник.Номенклатура";
			Результат = Запрос.Выполнить();
			Копия = Результат.Выгрузить();
			Копия.Очистить();
			Возврат Строка(Результат.Количество()) + "/" + Строка(Копия.Количество());
		КонецПроцедуры`

		assert.Equal(t, "1/0", runOnDB(t, db, ent, src),
			"очистка выгруженной коллекции не должна опустошать сам результат запроса")
	})
}
