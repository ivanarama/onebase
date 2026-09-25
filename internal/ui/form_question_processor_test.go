package ui

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
)

// #1683: формы обработок получают те же диалоговые билтины, что и формы
// сущностей. Фаза 1: Нажатие вызывает ПоказатьВопрос — вопрос доезжает до
// клиента; фаза 2: Ответ с _question_answer доставляет ВопросОтвет в
// обработчик.

func TestShowQuestion_ProcessorForm(t *testing.T) {
	formProgram := mustParse(t, `
Процедура ОформитьНажатие()
	ПоказатьВопрос("Продолжить?", "ДаНет", "Подтверждение");
КонецПроцедуры

Процедура ОформитьОтвет()
	Сообщить("Ответ: " + ВопросОтвет);
КонецПроцедуры
`)
	form := processorExecutionForm(
		&metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "Оформить",
			Handlers: map[metadata.FormEventType]string{
				metadata.FormEventOnClick:  "ОформитьНажатие",
				metadata.FormEventOnAnswer: "ОформитьОтвет",
			},
		},
	)
	form.ProgramAST = formProgram
	proc := &processor.Processor{Name: "ВопросОбработке", Forms: []*metadata.FormModule{form}}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)

	// Фаза 1: вопрос в ответе form-event обработки.
	body := processorClickBody("Оформить")
	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK {
		t.Fatalf("phase1 ok=false, error=%q", resp.Error)
	}
	if resp.Question == nil {
		t.Fatalf("processor form-event carries no question: %+v", resp)
	}
	if resp.Question.Text != "Продолжить?" || strings.Join(resp.Question.Variants, "|") != "Да|Нет" || resp.Question.Title != "Подтверждение" {
		t.Fatalf("question payload = %+v", resp.Question)
	}

	// Фаза 2: Ответ с _question_answer доставляет ВопросОтвет в обработчик.
	body2 := processorClickBody("Оформить")
	body2.Set("_event", string(metadata.FormEventOnAnswer))
	body2.Set("_question_answer", "Да")
	rec2 := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body2.Encode()))
	resp2 := decodeFormEventResponse(t, rec2.Body.Bytes())
	if !resp2.OK {
		t.Fatalf("phase2 ok=false, error=%q", resp2.Error)
	}
	if !strings.Contains(strings.Join(resp2.Messages, "\n"), "Ответ: Да") {
		t.Fatalf("Ответ handler did not receive ВопросОтвет: %v", resp2.Messages)
	}
}
