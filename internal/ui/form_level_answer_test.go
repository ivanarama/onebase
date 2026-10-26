package ui

import (
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Вопрос из ПриОткрытии («Принять задачу?» при открытии формы, как в 1С):
// у вопроса формы нет элемента-источника, клиент шлёт ответ событием Ответ
// без имени элемента. Раньше сервер отвечал «событие "Ответ" нельзя вызвать на
// уровне формы», и ответ пользователя терялся. Всё — через публичный form-event.
func TestFormLevelAnswer_QuestionFromOnOpen(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПриОткрытии()
	ПоказатьВопрос("Принять задачу?", "ДаНет");
КонецПроцедуры

Процедура ОтветНаВопрос()
	Если ВопросОтвет = "Да" Тогда
		Объект.Наименование = "принята";
	КонецЕсли;
	Сообщить("Ответ: " + ВопросОтвет);
КонецПроцедуры
`, map[metadata.FormEventType]string{
		metadata.FormEventOnOpen:   "ПриОткрытии",
		metadata.FormEventOnAnswer: "ОтветНаВопрос",
	}, nil)

	open := url.Values{}
	open.Set("_element", "")
	open.Set("_event", string(metadata.FormEventOnOpen))
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, open).Body.Bytes())
	if !resp.OK || resp.Question == nil {
		t.Fatalf("ПриОткрытии обязан вернуть вопрос: ok=%v error=%q question=%+v", resp.OK, resp.Error, resp.Question)
	}
	if strings.Join(resp.Question.Variants, "|") != "Да|Нет" {
		t.Fatalf("variants=%v", resp.Question.Variants)
	}

	answer := url.Values{}
	answer.Set("_element", "")
	answer.Set("_event", string(metadata.FormEventOnAnswer))
	answer.Set("_question_answer", "Да")
	answer.Set("Наименование", "новая")
	resp = decodeFormEventResponse(t, executeFormEvent(t, srv, ent, answer).Body.Bytes())
	if !resp.OK {
		t.Fatalf("ответ уровня формы отклонён: %q", resp.Error)
	}
	if !strings.Contains(strings.Join(resp.Messages, "\n"), "Ответ: Да") {
		t.Fatalf("обработчик Ответ не получил ВопросОтвет: %v", resp.Messages)
	}
	if got := resp.Values["Наименование"]; got != "принята" {
		t.Fatalf("изменение реквизита из Ответ не вернулось в форму: %v", got)
	}
}

// Fail-closed: без объявленного обработчика Ответ уровня формы не вызывается.
func TestFormLevelAnswer_UndeclaredHandlerRejected(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПриОткрытии()
	ПоказатьВопрос("Принять задачу?", "ДаНет");
КонецПроцедуры

Процедура ОтветНаВопрос()
	Сообщить("не должен выполниться");
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventOnOpen: "ПриОткрытии"}, nil)

	answer := url.Values{}
	answer.Set("_element", "")
	answer.Set("_event", string(metadata.FormEventOnAnswer))
	answer.Set("_question_answer", "Да")
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, answer).Body.Bytes())
	if resp.OK {
		t.Fatalf("Ответ без объявленного обработчика формы обязан быть отклонён: %+v", resp)
	}
	if !strings.Contains(resp.Error, "не объявлен") {
		t.Fatalf("error=%q", resp.Error)
	}
	if strings.Contains(strings.Join(resp.Messages, "\n"), "не должен выполниться") {
		t.Fatal("необъявленная процедура выполнилась")
	}
}
