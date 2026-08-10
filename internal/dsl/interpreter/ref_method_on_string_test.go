package interpreter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Колонка «Ссылка» в результате запроса даёт UUID-строку, а не ссылку с
// менеджером. Раньше ПолучитьОбъект() у такой строки молча возвращал
// Неопределено, и падало уже следующее обращение («Метод Записать вызван у
// Неопределено») — сообщение указывало не на ту строку, где ошибка. Теперь
// вызов сразу говорит, что делать.
func TestRefMethodOnString_RaisesWithHint(t *testing.T) {
	src := `Процедура Тест()
		Ссылка = "0b3f9a1e-2c4d-4a7b-9f10-2b6c8d1e5a30";
		Объект = Ссылка.ПолучитьОбъект();
	КонецПроцедуры`
	err := runProcErr(t, src)
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

// Граница правки: прочие методы у строки ведут себя как прежде (Неопределено,
// без ошибки) — диагностика добавлена только «ссылочным» методам, чтобы не
// менять поведение работающих конфигураций.
func TestOtherMethodOnString_Unchanged(t *testing.T) {
	src := `Процедура Тест()
		с = "текст";
		р = с.НеизвестныйМетод();
	КонецПроцедуры`
	assert.NoError(t, runProcErr(t, src))
}
