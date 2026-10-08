package ui

import (
	"fmt"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

// ПередЗакрытием исполняется в одном запросе close-intent: клиент ждёт решение
// «закрыть / оставить открытой» и больше ничего из ответа не показывает.
// Диалоговые билтины при этом работают в два хода — показать и дождаться
// второго события (Ответ, Выбор) или перейти на другую страницу, — и в закрытии
// второго хода нет. Раньше их payload уходил в ответ и молча терялся: с
// Отказ = Истина форма оставалась открытой без объяснения, без Отказа —
// закрывалась, так и не спросив. А ПоказатьВопрос в ПередЗакрытием — привычный
// приём из 1С. Явная ошибка обработчика честнее молчания: форма остаётся
// открытой, текст называет причину и обход.
var closeLifecycleDialogBuiltins = [][2]string{
	{"ПоказатьВопрос", "ShowQuestion"},
	{"ПоказатьПодбор", "ShowPicker"},
	{"ОткрытьФорму", "OpenForm"},
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
