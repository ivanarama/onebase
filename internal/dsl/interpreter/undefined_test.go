package interpreter_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Public expression execution, including host nil, must distinguish absence
// from every defined value. In particular, formatting must not supply identity.
func TestUndefinedEqualityAndOrder(t *testing.T) {
	for _, value := range []string{`0`, `-5`, `""`, `Ложь`, `"<nil>"`, `"{}"`, `Дата(2026, 1, 1)`} {
		t.Run(value, func(t *testing.T) {
			for _, expression := range []string{
				`Неопределено <> ` + value,
				value + ` <> Отсутствует`,
				`Пусто < ` + value,
				value + ` > Пусто`,
			} {
				require.Equal(t, true, evalExpr1136(t, expression, map[string]any{"Пусто": nil}), expression)
			}
		})
	}
	require.Equal(t, true, evalExpr1136(t, `Неопределено = Пусто И Пусто <= Неопределено И НЕ Пусто`, map[string]any{"Пусто": nil}))
}

func TestUndefinedMapPresenceAndCollectionSearch(t *testing.T) {
	const src = `Функция Т()
		С = Новый Соответствие;
		С.Вставить("первый", 0);
		С.Вставить("пустой", Неопределено);
		С.Вставить(Неопределено, "нет значения");
		С.Вставить("<nil>", "строка");
		С.Вставить(0, "ноль");
		М = [Неопределено, 0, "<nil>"];
		М[0] = Неопределено;
		Если С.Получить("нет") <> Неопределено Или С.Получить("первый") = Неопределено Тогда
			Возврат Ложь;
		КонецЕсли;
		Если НЕ С.СодержитКлюч("пустой") Или С.ContainsKey("нет") Тогда
			Возврат Ложь;
		КонецЕсли;
		Возврат С.Количество() = 5
			И С[Неопределено] = "нет значения" И С["<nil>"] = "строка" И С[0] = "ноль"
			И М.Найти(Неопределено) = 0 И М.Найти(0) = 1 И М.Найти("<nil>") = 2
			И М.Найти("нет") = Неопределено;
	КонецФункции`
	require.Equal(t, true, evalWithVars(t, src, nil))
}

func TestUndefinedCollectionOrdering(t *testing.T) {
	const src = `Функция Т()
		Т = Новый ТаблицаЗначений;
		Т.Колонки.Добавить("Значение");
		Т.Добавить().Значение = 0;
		Т.Добавить().Значение = Неопределено;
		Т.Добавить().Значение = -5;
		Т.Сортировать("Значение");
		М = Т.ВыгрузитьКолонку("Значение");
		Возврат М[0] = Неопределено И М[1] = -5 И М[2] = 0
			И Т.НайтиСтроки(Новый Структура("Значение", Неопределено)).Количество() = 1
			И Т.НайтиСтроки(Новый Структура("Значение", 0)).Количество() = 1;
	КонецФункции`
	require.Equal(t, true, evalWithVars(t, src, nil))
}
