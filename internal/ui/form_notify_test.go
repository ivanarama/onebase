package ui

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/realtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// «Вернуться в звонок» как в 1С: обработка (отдельная вкладка) по кнопке
// делает Оповестить("ВозвратВФормуЗвонка", …), а ОТКРЫТАЯ форма объекта в
// ОбработкаОповещения очищает поле — на своём текущем, несохранённом
// состоянии, без записи. Всё через публичные точки входа: form-event
// обработки → SSE-шина → form-event формы объекта.
func TestFormNotify_ProcessorNotifiesOpenObjectForm(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ОбработкаОповещения(ИмяСобытия, Параметр, Источник)
	Если ИмяСобытия = "ВозвратВФормуЗвонка" Тогда
		Объект.Наименование = "";
		Сообщить("Параметр: " + Параметр + "; источник: " + Источник);
	КонецЕсли;
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventNotify: "ОбработкаОповещения"}, nil)

	procForm := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "Вернуться",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "ВернутьсяНажатие"},
	})
	procForm.ProgramAST = mustParse(t, `
Процедура ВернутьсяНажатие()
	Оповестить("ВозвратВФормуЗвонка", "ОБ00000354", "ПоискЗаявок");
КонецПроцедуры
`)
	proc := &processor.Processor{Name: "ПоискЗаявок", Forms: []*metadata.FormModule{procForm}}
	srv.reg.LoadProcessors([]*processor.Processor{proc})
	srv.ops = newOperationLimiter()
	srv.hub = realtime.NewHub()
	_, events, cancel := srv.hub.Subscribe("", "", nil)
	defer cancel()

	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(processorClickBody("Вернуться").Encode()))
	if resp := decodeFormEventResponse(t, rec.Body.Bytes()); !resp.OK {
		t.Fatalf("кнопка обработки: %q", resp.Error)
	}
	id := waitFormNotify(t, events, "ВозвратВФормуЗвонка")

	// Открытая форма объекта со своей несохранённой правкой отвечает на
	// оповещение обычным событием формы.
	body := url.Values{}
	body.Set("_element", "")
	body.Set("_event", string(metadata.FormEventNotify))
	body.Set("_notify_id", id)
	body.Set("Наименование", "правка оператора")
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
	if !resp.OK {
		t.Fatalf("ОбработкаОповещения: %q", resp.Error)
	}
	if got := resp.Values["Наименование"]; got != "" {
		t.Fatalf("поле не очищено в открытой форме: %v", got)
	}
	if msg := strings.Join(resp.Messages, "\n"); !strings.Contains(msg, "Параметр: ОБ00000354; источник: ПоискЗаявок") {
		t.Fatalf("обработчик не получил ИмяСобытия/Параметр/Источник: %v", resp.Messages)
	}
	if resp.SavedID != "" {
		t.Fatalf("оповещение записало объект: savedId=%q", resp.SavedID)
	}
	if resp.Dirty == nil || !*resp.Dirty {
		t.Fatalf("форма после оповещения обязана остаться изменённой (запишет оператор): dirty=%v", resp.Dirty)
	}
	rows, err := srv.store.List(context.Background(), ent.Name, ent, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("оповещение не пишет объект, а в базе %d записей", len(rows))
	}

	// Одно оповещение получают все открытые формы пользователя: запись не
	// расходуется первой формой.
	if resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes()); !resp.OK {
		t.Fatalf("вторая форма того же пользователя: %q", resp.Error)
	}
}

// Оповещение адресовано формам того, кто выполнил Оповестить: чужой или
// выдуманный идентификатор — отказ, обработчик не выполняется.
func TestFormNotify_ForeignOrUnknownIDRejected(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ОбработкаОповещения()
	Сообщить("выполнился: " + ИмяСобытия);
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventNotify: "ОбработкаОповещения"}, nil)
	srv.hub = realtime.NewHub()
	op1 := &auth.User{ID: "u1", Login: "op1", IsAdmin: true}
	op2 := &auth.User{ID: "u2", Login: "op2", IsAdmin: true}
	_, events, cancel := srv.hub.Subscribe(op1.ID, op1.Login, nil)
	defer cancel()
	_, foreign, cancelForeign := srv.hub.Subscribe(op2.ID, op2.Login, nil)
	defer cancelForeign()

	srv.notifier(auth.ContextWithUser(context.Background(), op1)).(hubNotifier).NotifyForms("Событие", nil, nil)
	id := waitFormNotify(t, events, "Событие")
	select {
	case ev := <-foreign:
		t.Fatalf("оповещение op1 ушло формам op2: %+v", ev)
	default:
	}

	for name, tc := range map[string]struct {
		user *auth.User
		id   string
	}{
		"чужой пользователь":       {op2, id},
		"выдуманный идентификатор": {op1, "deadbeef"},
		"без идентификатора":       {op1, ""},
	} {
		body := url.Values{}
		body.Set("_element", "")
		body.Set("_event", string(metadata.FormEventNotify))
		body.Set("_notify_id", tc.id)
		resp := decodeFormEventResponse(t, executeFormEventAs(t, srv, ent, tc.user, body).Body.Bytes())
		if resp.OK || !strings.Contains(resp.Error, "оповещение формы устарело") {
			t.Fatalf("%s: ожидался отказ, ok=%v error=%q", name, resp.OK, resp.Error)
		}
		if strings.Contains(strings.Join(resp.Messages, "\n"), "выполнился") {
			t.Fatalf("%s: обработчик выполнился", name)
		}
	}

	body := url.Values{}
	body.Set("_element", "")
	body.Set("_event", string(metadata.FormEventNotify))
	body.Set("_notify_id", id)
	resp := decodeFormEventResponse(t, executeFormEventAs(t, srv, ent, op1, body).Body.Bytes())
	if !resp.OK || !strings.Contains(strings.Join(resp.Messages, "\n"), "выполнился: Событие") {
		t.Fatalf("своё оповещение: ok=%v error=%q messages=%v", resp.OK, resp.Error, resp.Messages)
	}
}

// Fail-closed: форма без обработчика ОбработкаОповещения событие не принимает.
func TestFormNotify_UndeclaredHandlerRejected(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ОбработкаОповещения()
	Сообщить("не должен выполниться");
КонецПроцедуры
`, nil, nil)
	srv.hub = realtime.NewHub()
	srv.notifier(context.Background()).(hubNotifier).NotifyForms("Событие", nil, nil)
	var id string
	for k := range srv.formNotifies().items {
		id = k
	}
	body := url.Values{}
	body.Set("_element", "")
	body.Set("_event", string(metadata.FormEventNotify))
	body.Set("_notify_id", id)
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
	if resp.OK || strings.Contains(strings.Join(resp.Messages, "\n"), "не должен выполниться") {
		t.Fatalf("необъявленный обработчик: ok=%v messages=%v", resp.OK, resp.Messages)
	}
}

// Запись живёт ограниченное время.
func TestFormNotifyStore_Expires(t *testing.T) {
	st := newFormNotifyStore()
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	st.now = func() time.Time { return now }
	id := st.put(&formNotification{login: "op1", event: "Е"})
	if _, ok := st.get(id, "op1"); !ok {
		t.Fatal("свежая запись не найдена")
	}
	now = now.Add(formNotifyTTL)
	if _, ok := st.get(id, "op1"); ok {
		t.Fatal("истёкшая запись выдана")
	}
}

func waitFormNotify(t *testing.T, events <-chan realtime.Event, event string) string {
	t.Helper()
	select {
	case ev := <-events:
		if ev.Name != formNotifyEvent {
			t.Fatalf("событие шины %q, ожидалось %q", ev.Name, formNotifyEvent)
		}
		data, _ := ev.Data.(map[string]any)
		if data["событие"] != event {
			t.Fatalf("данные оповещения %v", ev.Data)
		}
		id, _ := data["id"].(string)
		if id == "" {
			t.Fatalf("оповещение без id: %v", ev.Data)
		}
		return id
	case <-time.After(2 * time.Second):
		t.Fatal("оповещение не пришло в шину")
	}
	return ""
}

func executeFormEventAs(t *testing.T, s *Server, ent *metadata.Entity, user *auth.User, body url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/ui/catalog/"+ent.Name+"/form-event", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("kind", "catalog")
	rctx.URLParams.Add("entity", ent.Name)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	req = req.WithContext(auth.ContextWithUser(ctx, user))
	rec := httptest.NewRecorder()
	s.handleManagedFormEvent(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d, body=%s", rec.Code, rec.Body.String())
	}
	return rec
}

// Открытая форма ОБРАБОТКИ тоже получает оповещение — у неё свой маршрут
// form-event и свои служебные поля.
func TestFormNotify_ProcessorFormReceives(t *testing.T) {
	form := processorExecutionForm()
	form.Handlers = map[metadata.FormEventType]string{metadata.FormEventNotify: "ОбработкаОповещения"}
	form.ProgramAST = mustParse(t, `
Процедура ОбработкаОповещения()
	Сообщить("обработка получила: " + ИмяСобытия + " / " + Параметр);
КонецПроцедуры
`)
	proc := &processor.Processor{Name: "Приёмник", Forms: []*metadata.FormModule{form}}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	srv.hub = realtime.NewHub()
	_, events, cancel := srv.hub.Subscribe("", "", nil)
	defer cancel()
	srv.notifier(context.Background()).(hubNotifier).NotifyForms("Обновить", "42", nil)
	id := waitFormNotify(t, events, "Обновить")

	body := url.Values{}
	body.Set("_element", "")
	body.Set("_event", string(metadata.FormEventNotify))
	body.Set("_notify_id", id)
	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK || !strings.Contains(strings.Join(resp.Messages, "\n"), "обработка получила: Обновить / 42") {
		t.Fatalf("форма обработки: ok=%v error=%q messages=%v", resp.OK, resp.Error, resp.Messages)
	}

	body.Set("_notify_id", "deadbeef")
	rec = postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	if resp := decodeFormEventResponse(t, rec.Body.Bytes()); resp.OK {
		t.Fatal("выдуманный идентификатор в форме обработки принят")
	}
}
