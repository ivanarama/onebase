package ui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// ПоказатьВопрос в ПередЗакрытием — привычный приём из 1С («Вернуться в форму
// звонка?»). Первый ход: вопрос уходит в ответ close-intent, форма остаётся
// открытой. Клиент показывает его и повторяет закрытие с ВопросОтвет; второй
// ход решает — Отказ = Истина или закрыть. Повторный вопрос в отвеченном ходе —
// ошибка, иначе неудачный обработчик спрашивал бы бесконечно. Проверка — через
// смонтированный close-intent.

const beforeCloseAsksQuestion = `
Процедура ПроверитьЗакрытие(Отказ)
	Если ВопросОтвет = "" Тогда
		Варианты = Новый Массив;
		Варианты.Добавить("Вернуться к звонку");
		Варианты.Добавить("Закрыть — ошиблись номером");
		ПоказатьВопрос("Заявок не найдено. Вернуться к звонку?", Варианты);
		Возврат;
	КонецЕсли;
	Если ВопросОтвет <> "Закрыть — ошиблись номером" Тогда
		Отказ = Истина;
	КонецЕсли;
КонецПроцедуры
`

// checkCloseQuestionRounds — три хода через публичный close-intent: вопрос,
// ответ «остаться», ответ «закрыть».
func checkCloseQuestionRounds(t *testing.T, run func(answer string) formEventResponse) {
	t.Helper()
	first := run("")
	if first.Close == nil || first.Close.Allowed {
		t.Fatalf("вопрос задан, а форма закрыта: %+v", first)
	}
	if first.Error != "" {
		t.Fatalf("вопрос в ПередЗакрытием — не ошибка: %q", first.Error)
	}
	if first.Question == nil || first.Question.Text != "Заявок не найдено. Вернуться к звонку?" ||
		len(first.Question.Variants) != 2 {
		t.Fatalf("вопрос не дошёл до клиента: %+v", first.Question)
	}
	stay := run("Вернуться к звонку")
	if stay.Close == nil || stay.Close.Allowed || stay.Question != nil || stay.Error != "" {
		t.Fatalf("ответ «остаться»: форма должна остаться открытой без нового вопроса: %+v", stay)
	}
	leave := run("Закрыть — ошиблись номером")
	if leave.Close == nil || !leave.Close.Allowed || leave.Question != nil {
		t.Fatalf("ответ «закрыть»: форма должна закрыться: %+v", leave)
	}
}

func TestBeforeCloseQuestionAnswerRepeatsClose(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, beforeCloseAsksQuestion,
		map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	checkCloseQuestionRounds(t, func(answer string) formEventResponse {
		body := closeIntentBody(uuid.NewString(), "close", "Заявка")
		if answer != "" {
			body.Set("_question_answer", answer)
		}
		return decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, body))
	})
}

// В отвеченном ходе второй вопрос не задаётся: ошибка с причиной, форма
// остаётся открытой.
func TestBeforeCloseQuestionNotAskedTwice(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	ПоказатьВопрос("Закрыть?", "ДаНет");
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	body := closeIntentBody(uuid.NewString(), "close", "Заявка")
	body.Set("_question_answer", "Да")
	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, body))
	if response.Close == nil || response.Close.Allowed || response.Question != nil {
		t.Fatalf("повторный вопрос: форма должна остаться открытой без вопроса: %+v", response)
	}
	if !strings.Contains(response.Error, "ответ уже получен") {
		t.Fatalf("ошибка не называет причину: %q", response.Error)
	}
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
// вопрос работает так же: вопрос, ответ «остаться», ответ «закрыть».
func TestBeforeCloseQuestionInProcessorForm(t *testing.T) {
	form := processorExecutionForm()
	form.Handlers = map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}
	form.ProgramAST = mustParse(t, beforeCloseAsksQuestion)
	proc := &processor.Processor{Name: "ОбработкаСВопросом", Forms: []*metadata.FormModule{form}}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	checkCloseQuestionRounds(t, func(answer string) formEventResponse {
		body := processorCloseIntentBody(proc, uuid.NewString())
		if answer != "" {
			body.Set(processorServiceFieldName(proc.Params, "_question_answer"), answer)
		}
		return decodeCloseIntentResponse(t, executeProcessorCloseIntent(t, srv, proc, body))
	})
}

// Блокер круга 5 PR #1766: в пути формы обработки регистрация навигации стояла
// ПОСЛЕ disableDialogBuiltinsForClose и перезаписывала запрет. Через публичный
// close-intent обработчик получал рабочий ОткрытьФорму, ответ приходил с
// navigation.url и close.allowed=true — клиент закрытия переход не выполняет, и
// форма просто закрывалась, а обработчик «считал», что отправил человека на
// другой объект.
//
// Ссылка здесь настоящая, как в воспроизведении ревью: с Неопределено билтин
// падает на проверке типа аргумента и опасную картину (назначенный переход плюс
// разрешённое закрытие) не показывает. Проверяются оба имени — ОткрытьФорму и
// OpenForm: словарь билтинов у check общий, поэтому запрет обязан работать в
// рантайме, а не только в проверке конфигурации.
func TestBeforeCloseRejectsNavigationInProcessorForm(t *testing.T) {
	for _, builtin := range []string{"ОткрытьФорму", "OpenForm"} {
		t.Run(builtin, func(t *testing.T) {
			направления := &metadata.Entity{
				Name: "Направления", Kind: metadata.KindCatalog,
				Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
			}
			form := processorExecutionForm()
			form.Handlers = map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}
			form.ProgramAST = mustParse(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Ссылка = Справочники.Направления.НайтиПоНаименованию("Север");
	`+builtin+`(Ссылка);
КонецПроцедуры
`)
			proc := &processor.Processor{Name: "ОбработкаСНавигацией", Forms: []*metadata.FormModule{form}}

			ctx := context.Background()
			db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			if err := db.Migrate(ctx, []*metadata.Entity{направления}); err != nil {
				t.Fatal(err)
			}
			if err := db.Upsert(ctx, направления.Name, uuid.New(),
				map[string]any{"Наименование": "Север"}, направления); err != nil {
				t.Fatal(err)
			}
			registry := runtime.NewRegistry()
			registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{направления}})
			registry.LoadProcessors([]*processor.Processor{proc})
			interp := interpreter.New()
			interp.LookupProc = registry.GetModuleProc
			srv := &Server{
				store: db, reg: registry, interp: interp,
				lockMgr: runtime.NewLockManager(), messages: NewMessageStore(), ops: newOperationLimiter(),
			}
			srv.entitySvc = srv.newEntityService(nil)

			rec := executeProcessorCloseIntent(t, srv, proc, processorCloseIntentBody(proc, uuid.NewString()))
			response := decodeCloseIntentResponse(t, rec)

			if response.Close == nil || response.Close.Allowed {
				t.Fatalf("форма закрыта, хотя ПередЗакрытием вызвал навигацию: %+v", response)
			}
			// Переход в ответе закрытия — та самая опасная картина: клиент его не
			// выполнит, а форма закроется.
			if response.Navigation != nil {
				t.Fatalf("переход ушёл в ответ закрытия: %+v", response.Navigation)
			}
			if !strings.Contains(response.Error, "ОткрытьФорму недоступен в ПередЗакрытием") {
				t.Fatalf("ошибка не называет причину: %q", response.Error)
			}
		})
	}
}

// Обратная сторона запрета: в обычном событии формы обработки навигация обязана
// работать — иначе запрет закрыл бы саму возможность, ради которой PR и сделан.
func TestProcessorFormEventKeepsNavigationOutsideClose(t *testing.T) {
	направления := &metadata.Entity{
		Name: "Направления", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	for _, builtin := range []string{"ОткрытьФорму", "OpenForm"} {
		t.Run(builtin, func(t *testing.T) {
			form := processorExecutionForm(&metadata.FormElement{
				Kind: metadata.FormElementButton, Name: "Открыть",
				Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Нажатие"},
			})
			form.ProgramAST = mustParse(t, `
Процедура Нажатие()
	Ссылка = Справочники.Направления.НайтиПоНаименованию("Север");
	`+builtin+`(Ссылка);
КонецПроцедуры
`)
			proc := &processor.Processor{Name: "ОбработкаБезЗакрытия", Forms: []*metadata.FormModule{form}}

			ctx := context.Background()
			db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			if err := db.Migrate(ctx, []*metadata.Entity{направления}); err != nil {
				t.Fatal(err)
			}
			id := uuid.New()
			if err := db.Upsert(ctx, направления.Name, id,
				map[string]any{"Наименование": "Север"}, направления); err != nil {
				t.Fatal(err)
			}
			registry := runtime.NewRegistry()
			registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{направления}})
			registry.LoadProcessors([]*processor.Processor{proc})
			interp := interpreter.New()
			interp.LookupProc = registry.GetModuleProc
			srv := &Server{
				store: db, reg: registry, interp: interp,
				lockMgr: runtime.NewLockManager(), messages: NewMessageStore(), ops: newOperationLimiter(),
			}
			srv.entitySvc = srv.newEntityService(nil)

			body := processorClickBody("Открыть")
			rec := postProcessorFormEventExecution(t, srv, proc.Name,
				"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
			response := decodeFormEventResponse(t, rec.Body.Bytes())

			if !response.OK {
				t.Fatalf("обычное событие не выполнилось: %q", response.Error)
			}
			want := "/ui/catalog/Направления/" + id.String()
			if response.Navigation == nil || response.Navigation.URL != want {
				t.Fatalf("навигация в обычном событии потеряна: %+v, ожидался переход на %s",
					response.Navigation, want)
			}
		})
	}
}
