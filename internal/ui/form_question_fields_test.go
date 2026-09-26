package ui

import (
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

// Диалог показывает данные записанного документа и принимает ввод: без полей
// итог операции пришлось бы верстать отдельной формой, а её платформа открыть
// с параметрами не умеет.
func TestQuestionBuiltinAcceptsFields(t *testing.T) {
	var sink questionPayload
	fn := newQuestionBuiltin(&sink)

	поля := interpreter.NewArray([]any{
		interpreter.NewStructFromMap(map[string]any{
			"имя": "номер", "заголовок": "Постоянный номер", "значение": "Пл000000017", "выделить": true,
		}),
		interpreter.NewStructFromMap(map[string]any{
			"имя": "примечание", "заголовок": "Примечание", "значение": "перезвонить", "правка": true,
		}),
		// Строка без имени: получает позиционное, конфигурации незачем его писать.
		interpreter.NewStructFromMap(map[string]any{"заголовок": "Клиент", "значение": "Иванов"}),
	})

	варианты := interpreter.NewArray([]any{"Закрыть"})
	if _, err := fn([]any{"Заявка записана.", варианты, "Заявка принята", поля}, "", 0); err != nil {
		t.Fatalf("билтин отверг поля: %v", err)
	}
	if sink.Title != "Заявка принята" || len(sink.Fields) != 3 {
		t.Fatalf("payload собран неверно: %+v", sink)
	}
	if !sink.Fields[0].Strong || sink.Fields[0].Value != "Пл000000017" {
		t.Errorf("выделенное поле: %+v", sink.Fields[0])
	}
	if !sink.Fields[1].Editable || sink.Fields[1].Name != "примечание" {
		t.Errorf("редактируемое поле: %+v", sink.Fields[1])
	}
	if sink.Fields[2].Name == "" {
		t.Errorf("полю без имени не подставлено позиционное: %+v", sink.Fields[2])
	}
}

// Введённые значения возвращаются обработчику структурой ДиалогПоля.
func TestParseQuestionFields(t *testing.T) {
	st := parseQuestionFields(`{"примечание":"перезвонить после 18"}`)
	if st == nil {
		t.Fatal("значения полей не разобраны")
	}
	if got := st.Get("примечание"); got != "перезвонить после 18" {
		t.Errorf("значение поля: %v", got)
	}
	if parseQuestionFields("") != nil || parseQuestionFields("не json") != nil {
		t.Error("мусор должен давать nil, а не пустую структуру")
	}
}
