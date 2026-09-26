package ui

import (
	"encoding/json"
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
	Text     string          `json:"text"`
	Variants []string        `json:"variants"`
	Title    string          `json:"title,omitempty"`
	Fields   []questionField `json:"fields,omitempty"`
}

// questionField — строка данных в диалоге. Только чтение по умолчанию: диалог
// чаще показывает итог операции (номер записанного документа, клиент, дата),
// чем спрашивает ввод. Редактируемое поле возвращается обработчику в
// переменной ДиалогПоля вместе с нажатой кнопкой.
type questionField struct {
	Name     string `json:"name"`
	Label    string `json:"label,omitempty"`
	Value    string `json:"value,omitempty"`
	Editable bool   `json:"editable,omitempty"`
	Strong   bool   `json:"strong,omitempty"`
}

// newQuestionBuiltin строит билтин
// ПоказатьВопрос(Текст, Варианты[, Заголовок[, Поля]]).
// Варианты — известный набор (ДаНет / ДаНетОтмена / ОК, регистр не важен) или
// массив строк с произвольными подписями кнопок. Поля — массив структур
// {имя, заголовок, значение, правка, выделить}: диалог показывает их строками
// под текстом, а значения редактируемых возвращает в ДиалогПоля.
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
		var fields []questionField
		if len(args) >= 4 && args[3] != nil {
			var err error
			if fields, err = expandQuestionFields(args[3]); err != nil {
				return nil, err
			}
		}
		*sink = questionPayload{Text: text, Variants: variants, Title: title, Fields: fields}
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
	// Массив строк — произвольные подписи кнопок. Принимаем и готовый срез,
	// и Массив самого DSL: конфигурация пишет «Новый Массив», а он приходит
	// сюда как *interpreter.Array, и документированный «массив строк»
	// отвергался с «неизвестный набор вариантов "Массив[N]"».
	items, ok := v.([]any)
	if !ok {
		if arr, isArray := v.(interface{ Iterate() []any }); isArray {
			items, ok = arr.Iterate(), true
		}
	}
	if arr := items; ok {
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

// expandQuestionFields разбирает четвёртый аргумент билтина — массив структур
// с описанием строк диалога.
func expandQuestionFields(v any) ([]questionField, error) {
	items := iterateAny(v)
	if items == nil {
		return nil, fmt.Errorf("ПоказатьВопрос: Поля — массив структур {имя, заголовок, значение, правка, выделить}")
	}
	out := make([]questionField, 0, len(items))
	for i, item := range items {
		name := pickStr(dslField(item, "имя", "name"))
		if strings.TrimSpace(name) == "" {
			// Имя нужно только редактируемому полю — по нему обработчик
			// заберёт введённое значение. Для строки «только показать»
			// подставляем позиционное, чтобы конфигурация не писала лишнего.
			name = fmt.Sprintf("поле%d", i+1)
		}
		out = append(out, questionField{
			Name:     name,
			Label:    pickStr(dslField(item, "заголовок", "label", "title")),
			Value:    pickStr(dslField(item, "значение", "value")),
			Editable: asBool(dslField(item, "правка", "editable")),
			Strong:   asBool(dslField(item, "выделить", "strong")),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ПоказатьВопрос: список полей пуст")
	}
	return out, nil
}

// parseQuestionFields разбирает значения редактируемых полей, введённые
// пользователем в диалоге, в Структуру для обработчика события Ответ.
func parseQuestionFields(raw string) *interpreter.Struct {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(raw), &values); err != nil || len(values) == 0 {
		return nil
	}
	return interpreter.NewStructFromMap(values)
}
