package interpreter_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Колонка «Ссылка» в результате запроса даёт UUID-строку, а не ссылку с
// менеджером. Раньше ПолучитьОбъект() у такой строки молча возвращал
// Неопределено, и падало уже следующее обращение («Метод Записать вызван у
// Неопределено») — сообщение указывало не на ту строку, где ошибка. Теперь
// вызов сразу говорит, что делать.
func TestRefMethodOnString_RaisesWithHint(t *testing.T) {
	src := `Процедура Тест()
		Запрос = Новый Запрос;
		Запрос.Текст = "ВЫБРАТЬ Ссылка ИЗ Справочник.Номенклатура";
		Результат = Запрос.Выполнить();
		Стр = Результат[0];
		Объект = Стр.Ссылка.ПолучитьОбъект();
	КонецПроцедуры`
	prog, err := parser.New(lexer.New(src, "query-ref.os")).ParseProgram()
	require.NoError(t, err)
	require.NotEmpty(t, prog.Procedures)

	// Используем настоящий публичный путь Новый Запрос → Выполнить → строка
	// результата. Query materializer превращает SQL alias id в Стр.Ссылка и
	// именно там сохраняется UUID-строка, для которой нужна диагностика.
	db := &stubDB{rows: []map[string]any{{"id": "0b3f9a1e-2c4d-4a7b-9f10-2b6c8d1e5a30"}}}
	factory := interpreter.NewQueryFactory(context.Background(), db, &stubReg{})
	extra := map[string]any{"__factory_Запрос": factory}
	err = interpreter.New().Run(prog.Procedures[0], runtime.NewObject("Test", metadata.KindDocument), extra)
	require.Error(t, err)
	assert.ErrorContains(t, err, "вызван у строки")
	assert.ErrorContains(t, err, "НайтиПоИдентификатору")
}

// Удалить() у строки — та же ошибка: ссылку тоже надо получить у менеджера.
func TestRefDeleteOnString_RaisesWithHint(t *testing.T) {
	src := `Процедура Тест()
		Ссылка = "0b3f9a1e-2c4d-4a7b-9f10-2b6c8d1e5a30";
		Ссылка.Удалить();
	КонецПроцедуры`
	err := runProcErr(t, src)
	require.Error(t, err)
	assert.ErrorContains(t, err, "НайтиПоИдентификатору")
}

// После #718 любой неизвестный метод у строки является ошибкой. Специальная
// подсказка этого PR применяется только к ссылочным методам; остальные должны
// сохранить общую диагностику типа, а не советовать НайтиПоИдентификатору.
func TestOtherMethodOnString_UsesGeneralUnknownMethodError(t *testing.T) {
	src := `Процедура Тест()
		с = "текст";
		р = с.НеизвестныйМетод();
	КонецПроцедуры`
	err := runProcErr(t, src)
	require.Error(t, err)
	assert.ErrorContains(t, err, "не существует у значения типа Строка")
	assert.NotContains(t, err.Error(), "НайтиПоИдентификатору")
}
