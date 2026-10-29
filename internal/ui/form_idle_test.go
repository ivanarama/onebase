package ui

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
)

// Обработчик ожидания (ПодключитьОбработчикОжидания 1С) через публичный
// form-event: подключение в ПриОткрытии отдаёт браузеру команду с подписью,
// тик — обычное событие ОбработкаОжидания с текущими значениями формы.

const idleFormModule = `
Процедура ПриОткрытии()
	ПодключитьОбработчикОжидания("ОпросСостояния", 2);
	ПодключитьОбработчикОжидания("ОдинРаз", 0.2, Истина);
КонецПроцедуры

Процедура ОпросСостояния()
	Если Объект.Наименование = "ждёт" Тогда
		Объект.Наименование = "готово";
		ОтключитьОбработчикОжидания("ОпросСостояния");
	КонецЕсли;
КонецПроцедуры

Процедура ОдинРаз()
КонецПроцедуры

Процедура Скрытая()
	Объект.Наименование = "взломано";
КонецПроцедуры
`

func idleFormEvent(t *testing.T, srv *Server, ent *metadata.Entity, user *auth.User, values url.Values) formEventResponse {
	t.Helper()
	req := httptest.NewRequest("POST", "/ui/catalog/"+ent.Name+"/form-event", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("kind", "catalog")
	rctx.URLParams.Add("entity", ent.Name)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	if user != nil {
		ctx = auth.ContextWithUser(ctx, user)
	}
	rec := httptest.NewRecorder()
	srv.handleManagedFormEvent(rec, req.WithContext(ctx))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	return decodeFormEventResponse(t, rec.Body.Bytes())
}

func idleTick(proc, token, name string) url.Values {
	v := url.Values{}
	v.Set("_element", "")
	v.Set("_event", formIdleEvent)
	v.Set("_idle_proc", proc)
	v.Set("_idle_token", token)
	v.Set("Наименование", name)
	return v
}

func TestFormIdle_AttachTickDetachThroughFormEvent(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, idleFormModule,
		map[metadata.FormEventType]string{metadata.FormEventOnOpen: "ПриОткрытии"}, nil)
	op := &auth.User{ID: "u1", Login: "op1", IsAdmin: true}

	open := url.Values{}
	open.Set("_element", "")
	open.Set("_event", string(metadata.FormEventOnOpen))
	resp := idleFormEvent(t, srv, ent, op, open)
	if !resp.OK || len(resp.Idle) != 2 {
		t.Fatalf("ПриОткрытии обязан подключить два обработчика: ok=%v error=%q idle=%+v", resp.OK, resp.Error, resp.Idle)
	}
	poll, once := resp.Idle[0], resp.Idle[1]
	if poll.Proc != "ОпросСостояния" || poll.Interval != 2 || poll.Once || poll.Token == "" || poll.Off {
		t.Fatalf("команда опроса: %+v", poll)
	}
	if once.Proc != "ОдинРаз" || once.Interval != formIdleMinInterval || !once.Once {
		t.Fatalf("однократный с интервалом меньше секунды поднимается до 1 с: %+v", once)
	}

	// Тик без изменений не делает форму изменённой.
	resp = idleFormEvent(t, srv, ent, op, idleTick(poll.Proc, poll.Token, "просто"))
	if !resp.OK || resp.Dirty == nil || *resp.Dirty {
		t.Fatalf("тик без изменений: ok=%v error=%q dirty=%v", resp.OK, resp.Error, resp.Dirty)
	}
	// Тик видит текущие значения формы, меняет их и отключает себя.
	resp = idleFormEvent(t, srv, ent, op, idleTick(poll.Proc, poll.Token, "ждёт"))
	if !resp.OK || resp.Values["Наименование"] != "готово" || resp.Dirty == nil || !*resp.Dirty {
		t.Fatalf("тик с изменением: ok=%v error=%q values=%v dirty=%v", resp.OK, resp.Error, resp.Values, resp.Dirty)
	}
	if len(resp.Idle) != 1 || !resp.Idle[0].Off || resp.Idle[0].Proc != "ОпросСостояния" {
		t.Fatalf("ОтключитьОбработчикОжидания не дошло до браузера: %+v", resp.Idle)
	}
}

// Браузер может вызвать тиком только подключённую модулем процедуру и только
// от того же пользователя: иначе событие тика открыло бы любую процедуру
// модуля формы.
func TestFormIdle_ForgedTickRejected(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, idleFormModule,
		map[metadata.FormEventType]string{metadata.FormEventOnOpen: "ПриОткрытии"}, nil)
	op1 := &auth.User{ID: "u1", Login: "op1", IsAdmin: true}
	op2 := &auth.User{ID: "u2", Login: "op2", IsAdmin: true}
	open := url.Values{}
	open.Set("_element", "")
	open.Set("_event", string(metadata.FormEventOnOpen))
	poll := idleFormEvent(t, srv, ent, op1, open).Idle[0]

	for name, tc := range map[string]struct {
		user        *auth.User
		proc, token string
	}{
		"чужая процедура с подписью опроса": {op1, "Скрытая", poll.Token},
		"без подписи":                  {op1, "Скрытая", ""},
		"подпись другого пользователя": {op2, poll.Proc, poll.Token},
		"выдуманная подпись":           {op1, poll.Proc, strings.Repeat("0", 64)},
	} {
		resp := idleFormEvent(t, srv, ent, tc.user, idleTick(tc.proc, tc.token, "ждёт"))
		if resp.OK || resp.Values["Наименование"] == "взломано" || resp.Values["Наименование"] == "готово" {
			t.Fatalf("%s: тик принят: %+v", name, resp)
		}
		if len(resp.Idle) != 1 || !resp.Idle[0].Off {
			t.Fatalf("%s: отклонённый тик обязан снять таймер в браузере: %+v", name, resp.Idle)
		}
	}
}

func TestFormIdle_AttachUnknownProcIsError(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПриОткрытии()
	ПодключитьОбработчикОжидания("НетТакой", 2);
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventOnOpen: "ПриОткрытии"}, nil)
	open := url.Values{}
	open.Set("_element", "")
	open.Set("_event", string(metadata.FormEventOnOpen))
	resp := idleFormEvent(t, srv, ent, nil, open)
	if resp.OK || !strings.Contains(resp.Error, "НетТакой") || len(resp.Idle) != 0 {
		t.Fatalf("подключение несуществующей процедуры: ok=%v error=%q idle=%+v", resp.OK, resp.Error, resp.Idle)
	}
}

// Форма обработки («Поиск заявок»): свой маршрут form-event и служебные поля.
func TestFormIdle_ProcessorForm(t *testing.T) {
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "Начать",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "НачатьНажатие"},
	})
	form.ProgramAST = mustParse(t, `
Процедура НачатьНажатие()
	ПодключитьОбработчикОжидания("Опрос", 3);
КонецПроцедуры

Процедура Опрос()
	Сообщить("тик обработки");
КонецПроцедуры
`)
	proc := &processor.Processor{Name: "ПоискЗаявок", Forms: []*metadata.FormModule{form}}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)

	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(processorClickBody("Начать").Encode()))
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK || len(resp.Idle) != 1 || resp.Idle[0].Proc != "Опрос" {
		t.Fatalf("подключение в форме обработки: ok=%v error=%q idle=%+v", resp.OK, resp.Error, resp.Idle)
	}
	tick := url.Values{}
	tick.Set("_element", "")
	tick.Set("_event", formIdleEvent)
	tick.Set("_idle_proc", "Опрос")
	tick.Set("_idle_token", resp.Idle[0].Token)
	rec = postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(tick.Encode()))
	resp = decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK || !strings.Contains(strings.Join(resp.Messages, "\n"), "тик обработки") {
		t.Fatalf("тик формы обработки: ok=%v error=%q messages=%v", resp.OK, resp.Error, resp.Messages)
	}

	// Подпись формы обработки не годится другой форме (другой владелец).
	tick.Set("_idle_token", srv.formIdleToken("", "entity:ПоискЗаявок", form.Name, "Опрос"))
	rec = postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(tick.Encode()))
	if resp := decodeFormEventResponse(t, rec.Body.Bytes()); resp.OK {
		t.Fatal("подпись чужой формы принята")
	}
}
