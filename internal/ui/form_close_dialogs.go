package ui

import (
	"fmt"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

// ПередЗакрытием исполняется в одном запросе close-intent: клиент ждёт решение
// «закрыть / оставить открытой». Подбор и переход на другую форму работают в два
// хода — показать и дождаться второго события (Выбор) или уйти со страницы, — и
// в закрытии второго хода у них нет. Раньше их payload уходил в ответ и молча
// терялся; явная ошибка обработчика честнее молчания: форма остаётся открытой,
// текст называет причину и обход.
//
// ПоказатьВопрос в ПередЗакрытием — привычный приём из 1С («Вернуться в форму
// звонка?»), и у него второй ход есть: вопрос оставляет форму открытой, клиент
// показывает его и по ответу повторяет закрытие с ВопросОтвет, а обработчик
// решает — Отказ = Истина или закрыть (closeQuestionBuiltin).
var closeLifecycleDialogBuiltins = [][2]string{
	{"ПоказатьПодбор", "ShowPicker"},
	{"ОткрытьФорму", "OpenForm"},
}

// closeQuestionBuiltin — ПоказатьВопрос в ПередЗакрытием. Повтор закрытия уже
// несёт ответ (answered): второй вопрос в нём не задаётся, иначе неудачно
// написанный обработчик спрашивал бы бесконечно.
func closeQuestionBuiltin(ask interpreter.BuiltinFunc, answered bool) interpreter.BuiltinFunc {
	if !answered {
		return ask
	}
	return interpreter.BuiltinFunc(func(_ []any, _ string, _ int) (any, error) {
		return nil, fmt.Errorf("ПоказатьВопрос в ПередЗакрытием: ответ уже получен (ВопросОтвет) — второй вопрос в том же закрытии не задаётся")
	})
}

// allowCloseQuestion подключает ПоказатьВопрос в окружение ПередЗакрытием.
func allowCloseQuestion(vars map[string]any, ask interpreter.BuiltinFunc, answered bool) {
	fn := closeQuestionBuiltin(ask, answered)
	vars["ПоказатьВопрос"] = fn
	vars["ShowQuestion"] = fn
}

// disableDialogBuiltinsForClose заменяет в окружении ПередЗакрытием диалоговые
// билтины на отказ с понятным текстом.
func disableDialogBuiltinsForClose(vars map[string]any) {
	for _, names := range closeLifecycleDialogBuiltins {
		fn := closeDialogUnavailable(names[0])
		for _, name := range names {
			vars[name] = fn
		}
	}
}

func closeDialogUnavailable(name string) interpreter.BuiltinFunc {
	return interpreter.BuiltinFunc(func(_ []any, _ string, _ int) (any, error) {
		return nil, fmt.Errorf(
			"%s недоступен в ПередЗакрытием: закрытие формы не ждёт ответа пользователя. Назовите причину через Сообщить и оставьте форму открытой (Отказ = Истина), а вопрос задайте командой формы",
			name)
	})
}
