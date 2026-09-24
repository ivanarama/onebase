package ui

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

// ПоказатьВопрос (#1528, план 158 срез A): двухфазный диалог «предупреждение
// с продолжением» по образцу ПоказатьПодбор. Фаза 1: билтин копит payload в
// sink, после Run он уходит в ответ как question, и клиент рисует модал.
// Фаза 2: ответ пользователя приезжает событием Ответ с переменной ВопросОтвет.
// Диалог неблокирующий: обработчик фазы 1 обязан завершиться (Возврат), как и
// у ПоказатьПодбор.

// questionPayload уходит клиенту в ответе form-event.
type questionPayload struct {
	Text     string   `json:"text"`
	Variants []string `json:"variants"`
	Title    string   `json:"title,omitempty"`
}

// newQuestionBuiltin строит билтин ПоказатьВопрос(Текст, Варианты[, Заголовок]).
// Варианты — известный набор (ДаНет / ДаНетОтмена / ОК, регистр не важен) или
// массив строк с произвольными подписями кнопок.
func newQuestionBuiltin(sink *questionPayload) interpreter.BuiltinFunc {
	return interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("ПоказатьВопрос(Текст, Варианты[, Заголовок]): ожидалось не менее 2 аргументов, получено %d", len(args))
		}
		text := strings.TrimSpace(fmt.Sprintf("%v", args[0]))
		if text == "" || text == "<nil>" {
			return nil, fmt.Errorf("ПоказатьВопрос: текст вопроса пуст")
		}
		variants, err := expandQuestionVariants(args[1])
		if err != nil {
			return nil, err
		}
		title := ""
		if len(args) >= 3 {
			title = strings.TrimSpace(fmt.Sprintf("%v", args[2]))
		}
		*sink = questionPayload{Text: text, Variants: variants, Title: title}
		return nil, nil
	})
}

// knownQuestionVariants — минимальный набор наборов кнопок первого среза.
// Ключ — то, что конфигурация пишет вторым аргументом; значение — подписи
// кнопок по порядку.
var knownQuestionVariants = map[string][]string{
	"данет":       {"Да", "Нет"},
	"данетотмена": {"Да", "Нет", "Отмена"},
	"ок":          {"ОК"},
}

// expandQuestionVariants разбирает второй аргумент билтина.
func expandQuestionVariants(v any) ([]string, error) {
	s := strings.TrimSpace(fmt.Sprintf("%v", v))
	if token, ok := knownQuestionVariants[strings.ToLower(s)]; ok {
		return append([]string(nil), token...), nil
	}
	// Массив строк — произвольные подписи кнопок.
	if arr, ok := v.([]any); ok {
		var out []string
		for _, item := range arr {
			label := strings.TrimSpace(fmt.Sprintf("%v", item))
			if label == "" {
				return nil, fmt.Errorf("ПоказатьВопрос: пустая подпись кнопки в списке вариантов")
			}
			out = append(out, label)
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("ПоказатьВопрос: список вариантов пуст")
		}
		return out, nil
	}
	return nil, fmt.Errorf("ПоказатьВопрос: неизвестный набор вариантов %q (ДаНет, ДаНетОтмена, ОК или массив строк)", s)
}
