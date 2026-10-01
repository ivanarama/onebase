package ui

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
)

// ПоказатьВопрос в ПередЗакрытием — привычный приём из 1С, но закрытие ждёт
// один ответ сервера и второго хода (событие Ответ) у него нет. Раньше вопрос
// уходил в ответ close-intent и молча терялся: форма с Отказ = Истина просто не
// закрывалась, без объяснения. Теперь вызов — ошибка обработчика с причиной, а
// форма остаётся открытой. Проверка — через смонтированный close-intent.

const beforeCloseAsksQuestion = `
Процедура ПроверитьЗакрытие(Отказ)
	Отказ = Истина;
	ПоказатьВопрос("Документ не проведён. Закрыть?", "ДаНет");
КонецПроцедуры
`

func assertCloseDialogRejected(t *testing.T, response formEventResponse, builtin string) {
	t.Helper()
	if response.Close == nil || response.Close.Allowed {
		t.Fatalf("форма закрыта, хотя ПередЗакрытием не смог показать диалог: %+v", response)
	}
	if response.Question != nil {
		t.Fatalf("вопрос ушёл в ответ закрытия, где его никто не покажет: %+v", response.Question)
	}
	if !strings.Contains(response.Error, builtin+" недоступен в ПередЗакрытием") {
		t.Fatalf("ошибка не называет причину: %q", response.Error)
	}
}

func TestBeforeCloseRejectsQuestionDialog(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, beforeCloseAsksQuestion,
		map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	rec := executeFormCloseIntent(t, srv, ent, closeIntentBody(uuid.NewString(), "close", "Заявка"))
	assertCloseDialogRejected(t, decodeCloseIntentResponse(t, rec), "ПоказатьВопрос")
}

func TestBeforeCloseRejectsNavigationAndPicker(t *testing.T) {
	for builtin, call := range map[string]string{
		"ОткрытьФорму":   `ОткрытьФорму(Неопределено);`,
		"ПоказатьПодбор": `ПоказатьПодбор(Новый Структура);`,
	} {
		t.Run(builtin, func(t *testing.T) {
			srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	`+call+`
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
			rec := executeFormCloseIntent(t, srv, ent, closeIntentBody(uuid.NewString(), "close", "Заявка"))
			response := decodeCloseIntentResponse(t, rec)
			if response.Close == nil || response.Close.Allowed {
				t.Fatalf("форма закрыта: %+v", response)
			}
			if !strings.Contains(response.Error, builtin+" недоступен в ПередЗакрытием") {
				t.Fatalf("ошибка не называет причину: %q", response.Error)
			}
		})
	}
}

// Формы обработок получили диалоговые билтины в #1697 — в их ПередЗакрытием
// тот же отказ.
func TestBeforeCloseRejectsQuestionDialogInProcessorForm(t *testing.T) {
	form := processorExecutionForm()
	form.Handlers = map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}
	form.ProgramAST = mustParse(t, beforeCloseAsksQuestion)
	proc := &processor.Processor{Name: "ОбработкаСВопросом", Forms: []*metadata.FormModule{form}}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	rec := executeProcessorCloseIntent(t, srv, proc, processorCloseIntentBody(proc, uuid.NewString()))
	assertCloseDialogRejected(t, decodeCloseIntentResponse(t, rec), "ПоказатьВопрос")
}
