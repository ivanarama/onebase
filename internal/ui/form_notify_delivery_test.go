package ui

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/realtime"
)

func notifyDeliveryBody(id string) url.Values {
	return url.Values{"_element": {""}, "_event": {string(metadata.FormEventNotify)}, "_notify_id": {id}}
}

func notifySenderButton() *metadata.FormElement {
	return &metadata.FormElement{Kind: metadata.FormElementButton, Name: "Отправить",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Отправить"}}
}

func TestFormNotify_PositionalArguments(t *testing.T) {
	for _, strict := range []bool{false, true} {
		for _, timeout := range []int{0, 5} {
			for _, route := range []string{"entity", "processor"} {
				t.Run(fmt.Sprintf("%s/strict=%v/timeout=%d", route, strict, timeout), func(t *testing.T) {
					// Deliberately use names other than the global aliases.
					source := `
Процедура Принять(Имя, Данные, Отправитель)
  Сообщить(Имя + "/" + Данные + "/" + Отправитель);
КонецПроцедуры
Процедура Отправить()
  Оповестить("Готово", "параметр", "источник");
КонецПроцедуры`
					s, ent := setupManagedEventsServer(t, source,
						map[metadata.FormEventType]string{metadata.FormEventNotify: "Принять"}, []*metadata.FormElement{notifySenderButton()})
					s.interp.StrictLexicalScope = strict
					s.cfg.Limits.RequestTimeoutSec = timeout
					s.hub = realtime.NewHub()
					_, events, cancel := s.hub.Subscribe("", "", nil)
					defer cancel()
					resp := decodeFormEventResponse(t, executeFormEvent(t, s, ent, processorClickBody("Отправить")).Body.Bytes())
					if !resp.OK {
						t.Fatalf("sender: %s", resp.Error)
					}
					body := notifyDeliveryBody(waitFormNotify(t, events, "Готово"))
					if route == "entity" {
						resp = decodeFormEventResponse(t, executeFormEvent(t, s, ent, body).Body.Bytes())
					} else {
						form := processorExecutionForm()
						form.Handlers = map[metadata.FormEventType]string{metadata.FormEventNotify: "Принять"}
						form.ProgramAST = mustParse(t, source)
						proc := &processor.Processor{Name: "Приёмник", Forms: []*metadata.FormModule{form}}
						s.reg.LoadProcessors([]*processor.Processor{proc})
						resp = decodeFormEventResponse(t, postProcessorFormEventExecution(t, s, proc.Name,
							"application/x-www-form-urlencoded", strings.NewReader(body.Encode())).Body.Bytes())
					}
					if !resp.OK || strings.Join(resp.Messages, "") != "Готово/параметр/источник" {
						t.Fatalf("receiver: ok=%v error=%s messages=%v", resp.OK, resp.Error, resp.Messages)
					}
				})
			}
		}
	}
}

func TestFormNotify_ReferenceUsesReceiverTransaction(t *testing.T) {
	s, ent := setupManagedEventsServer(t, `
Процедура Отправить()
  Запись = Справочники.Контрагент.Создать();
  Запись.Наименование = "исходное";
  Запись.Телефон = "старый";
  Запись.Записать();
  Оповестить("Ссылка", Запись.Ссылка, Новый Структура("Вложенная", Запись.Ссылка));
КонецПроцедуры
Процедура Принять()
  НачатьТранзакцию();
  Запись = Параметр.ПолучитьОбъект();
  Сообщить(Запись.Наименование);
  Запись.Наименование = "в транзакции";
  Запись.Телефон = "новый";
  Запись.Записать();
  Другая = Источник.Вложенная.ПолучитьОбъект();
  Сообщить(Другая.Наименование);
  Сообщить(Источник.Вложенная.Телефон);
  ОтменитьТранзакцию();
  После = Параметр.ПолучитьОбъект();
  Сообщить(После.Наименование);
КонецПроцедуры`, map[metadata.FormEventType]string{metadata.FormEventNotify: "Принять"}, []*metadata.FormElement{notifySenderButton()})
	ent.Fields = append(ent.Fields, metadata.Field{Name: "Телефон", Type: metadata.FieldTypeString})
	if err := s.store.Migrate(context.Background(), []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	s.cfg.Limits.RequestTimeoutSec = 5
	s.hub = realtime.NewHub()
	_, events, cancel := s.hub.Subscribe("", "", nil)
	defer cancel()
	resp := decodeFormEventResponse(t, executeFormEvent(t, s, ent, processorClickBody("Отправить")).Body.Bytes())
	if !resp.OK {
		t.Fatalf("sender: %s", resp.Error)
	}
	// The sender's request and its DSL context have already ended.
	body := notifyDeliveryBody(waitFormNotify(t, events, "Ссылка"))
	resp = decodeFormEventResponse(t, executeFormEvent(t, s, ent, body).Body.Bytes())
	want := "исходное/в транзакции/новый/исходное"
	if !resp.OK || strings.Join(resp.Messages, "/") != want {
		t.Fatalf("receiver: ok=%v error=%s messages=%v", resp.OK, resp.Error, resp.Messages)
	}
}

func TestFormNotify_MutablePayloadIsolated(t *testing.T) {
	s, ent := setupManagedEventsServer(t, `
Процедура Отправить()
  Данные = Новый Структура("Счётчик", 0);
  Массив = Новый Массив;
  Массив.Добавить(Данные);
  Карта = Новый Соответствие;
  Карта.Вставить("ключ", Данные);
  Таблица = Новый ТаблицаЗначений;
  Таблица.Колонки.Добавить("Данные");
  Строка = Таблица.Добавить();
  Строка.Данные = Данные;
  Данные.Вставить("Сам", Данные);
  Пакет = Новый Структура("Массив,Карта,Таблица,Строка", Массив, Карта, Таблица, Строка);
  Оповестить("Структура", Данные, Пакет);
  Данные.Счётчик = 99;
  Массив.Очистить();
  Карта.Очистить();
  Таблица.Очистить();
КонецПроцедуры
Процедура Принять()
  Сообщить(Параметр.Счётчик);
  Параметр.Счётчик = 7;
  Сообщить(Параметр.Сам.Счётчик);
  Сообщить(Источник.Массив[0].Счётчик);
  Сообщить(Источник.Карта.Получить("ключ").Счётчик);
  Сообщить(Источник.Таблица.Получить(0).Данные.Счётчик);
  Источник.Строка.Данные.Счётчик = 8;
  Сообщить(Параметр.Счётчик);
КонецПроцедуры`, map[metadata.FormEventType]string{metadata.FormEventNotify: "Принять"}, []*metadata.FormElement{notifySenderButton()})
	s.hub = realtime.NewHub()
	_, events, cancel := s.hub.Subscribe("", "", nil)
	defer cancel()
	resp := decodeFormEventResponse(t, executeFormEvent(t, s, ent, processorClickBody("Отправить")).Body.Bytes())
	if !resp.OK {
		t.Fatalf("sender: %s", resp.Error)
	}
	id := waitFormNotify(t, events, "Структура")
	// All forms must receive the original snapshot, including repeated delivery.
	for range 2 {
		resp = decodeFormEventResponse(t, executeFormEvent(t, s, ent, notifyDeliveryBody(id)).Body.Bytes())
		if !resp.OK || strings.Join(resp.Messages, "/") != "0/7/7/7/7/8" {
			t.Fatalf("receiver: ok=%v error=%s messages=%v", resp.OK, resp.Error, resp.Messages)
		}
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			got := decodeFormEventResponse(t, executeFormEvent(t, s, ent, notifyDeliveryBody(id)).Body.Bytes())
			if !got.OK || strings.Join(got.Messages, "/") != "0/7/7/7/7/8" {
				t.Errorf("concurrent receiver: ok=%v error=%s messages=%v", got.OK, got.Error, got.Messages)
			}
		})
	}
	wg.Wait()
}

func TestFormNotify_RejectsUncopyablePayloadBeforePublication(t *testing.T) {
	for _, tc := range []struct {
		name, source, message string
	}{
		{"live object", `Оповестить("Ошибка", Объект);`, "нельзя передать другой форме"},
		{"depth limit", `Данные = Неопределено;
Для i = 1 По 70 Цикл
  Данные = Новый Структура("Следующий", Данные);
КонецЦикла;
Оповестить("Ошибка", Данные);`, "превышает предел копирования"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ent := setupManagedEventsServer(t, "Процедура Отправить()\n"+tc.source+"\nКонецПроцедуры",
				nil, []*metadata.FormElement{notifySenderButton()})
			s.hub = realtime.NewHub()
			_, events, cancel := s.hub.Subscribe("", "", nil)
			defer cancel()
			resp := decodeFormEventResponse(t, executeFormEvent(t, s, ent, processorClickBody("Отправить")).Body.Bytes())
			if resp.OK || !strings.Contains(resp.Error, tc.message) {
				t.Fatalf("expected a copy error: ok=%v error=%s", resp.OK, resp.Error)
			}
			select {
			case ev := <-events:
				t.Fatalf("failed snapshot published an event: %+v", ev)
			default:
			}
			if len(s.formNotifies().items) != 0 {
				t.Fatal("failed snapshot left a notification record")
			}
		})
	}
}
