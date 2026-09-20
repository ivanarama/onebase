package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// The same public runner used by `onebase procrun` must support both idioms
// together: a missing map value differs from zero, an empty number field does
// not. Neither reading a typed empty value nor assigning Undefined leaks the
// interpreter representation to the database.
func TestProcrunUndefinedAndEmptyNumber(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"catalogs/Проба.yaml":      "name: Проба\nfields:\n  - name: Сумма\n    type: number\n",
		"processors/Проверка.yaml": "name: Проверка\n",
		"src/Проверка.proc.os": `Процедура Выполнить()
			С = Новый Соответствие;
			С.Вставить("первая", 0);
			С.Вставить("пустая", Неопределено);
			Если С.Получить("первая") = Неопределено Или С.Получить("нет") = 0 Тогда
				ВызватьИсключение("Ноль и отсутствие смешаны");
			КонецЕсли;
			Если С.Получить("нет") <> Неопределено Или НЕ С.СодержитКлюч("пустая") Или С.СодержитКлюч("нет") Тогда
				ВызватьИсключение("Наличие ключа потеряно");
			КонецЕсли;
			Об = Справочники.Проба.Создать();
			Если Об.Сумма <> 0 Или Об.Сумма = Неопределено Тогда
				ВызватьИсключение("Пустое число нового объекта");
			КонецЕсли;
			Об.Сумма = Неопределено;
			Ссылка = Об.Записать();
			Загружен = Ссылка.ПолучитьОбъект();
			Если Загружен.Сумма <> 0 Или Загружен.Сумма = Неопределено Тогда
				ВызватьИсключение("Пустое число загруженного объекта");
			КонецЕсли;
			Запрос = Новый Запрос;
			Запрос.Текст = "ВЫБРАТЬ Сумма ИЗ Справочник.Проба ГДЕ &Пусто ЕСТЬ ПУСТО";
			Запрос.УстановитьПараметр("Пусто", Неопределено);
			Стр = Запрос.Выполнить()[0];
			Если ТипЗнч(Стр.Сумма) <> "Число" Или Стр.Сумма <> 0 Или Стр.Сумма = Неопределено Тогда
				ВызватьИсключение("Пустое число запроса");
			КонецЕсли;
			Сообщить("ok");
		КонецПроцедуры`,
	}
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	proj, err := project.Load(dir)
	require.NoError(t, err)
	defer proj.Close()
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		require.NoError(t, db.Migrate(ctx, proj.Entities))
		messages, runErr, err := RunProcessorOffline(ctx, proj, db, "Проверка", nil, nil)
		require.NoError(t, err)
		require.NoError(t, runErr)
		require.Equal(t, []string{"ok"}, messages)
		compiled, err := query.Compile(`ВЫБРАТЬ Сумма ИЗ Справочник.Проба`, query.CompileOpts{
			Entities: proj.Entities, Dialect: db.Dialect(),
		})
		require.NoError(t, err)
		rows, _, err := query.Run(ctx, db, &compiled)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Nil(t, rows[0]["сумма"], "host query/storage must retain SQL NULL")
	})
}
