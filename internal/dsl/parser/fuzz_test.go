package parser_test

import (
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
)

// FuzzParseProgram — QUAL-03 / issue #792. Парсер DSL обрабатывает сложный и
// частично недоверенный ввод (конфигурации, редактор модулей в браузере).
// Контракт: на ЛЮБОМ входе ParseProgram возвращает program либо ошибку, но
// никогда не паникует и не зацикливается. Fuzz прогоняет lexer+parser вместе.
//
// Короткий smoke-прогон подходит для CI (go test -run=Fuzz гоняет только seed'ы);
// длительный fuzz запускается отдельно: go test -fuzz=FuzzParseProgram.
func FuzzParseProgram(f *testing.F) {
	seeds := []string{
		"",
		"Процедура П() КонецПроцедуры",
		"Функция Ф() Возврат 1 + 2 * 3; КонецФункции",
		"Если Истина Тогда Сообщить(\"привет\"); Иначе Прервать; КонецЕсли;",
		"Для Каждого Элемент Из Коллекция Цикл Продолжить; КонецЦикла;",
		"Пока Х < 10 Цикл Х = Х + 1; КонецЦикла;",
		"А = Новый Структура(\"ключ\", 42);",
		"Попытка ВызватьИсключение(\"oops\"); Исключение Сообщить(\"err\"); КонецПопытки;",
		"Б = [1, 2, 3]; В = Б[0];",
		"Функция Ф(а, б = 5) Возврат а + б; КонецФункции",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		// Значение не важно: важно, что вызов завершается без паники.
		// Паника на любом входе — дефект, который fuzz зафиксирует как crasher.
		_, _ = parser.New(lexer.New(src, "fuzz.os")).ParseProgram()
	})
}
