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

// Форма обработки «Создание задачи» cc2: служебный флаг СкрытьМастера —
// параметр без элемента, от него зависит hidden_when поля мастера. Раньше
// событие, не пересчитавшее флаг, видело его пустым, и поле вылезало. А номер
// звонящего, положенный обработчиком в параметр без элемента, уходил оператору
// в ответе формы открытым текстом. Путь — публичный /form-event, как браузер:
// ответ первого события несёт токен, второе событие присылает его обратно.
func TestProcessorFormCarry_FlagSurvivesAndHiddenValueIsSealed(t *testing.T) {
	formProgram := mustParse(t, `
Процедура Установить()
	Объект.СкрытьМастера = Истина;
	Объект.Номер = "+79161234455";
	Объект.Звонок = "звонок-17";
КонецПроцедуры

Процедура Проверить()
	Номер = "нет";
	Если Объект.Номер = "+79161234455" Тогда
		Номер = "номер";
	КонецЕсли;
	Сообщить(Строка(Объект.СкрытьМастера) + "|" + Номер + "|" + Объект.Звонок + "|" + Объект.Комментарий);
КонецПроцедуры
`)
	form := processorExecutionForm(
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ПолеКомментарий", DataPath: "Объект.Комментарий"},
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ПолеЗвонок", DataPath: "Объект.Звонок", ReadOnly: true},
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ПолеМастер", DataPath: "Объект.Мастер", HiddenWhen: "СкрытьМастера"},
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "КнопкаУстановить",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Установить"}},
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "КнопкаПроверить",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Проверить"}},
	)
	form.ProgramAST = formProgram
	proc := &processor.Processor{
		Name: "СозданиеЗадачи",
		Params: []processor.Param{
			{Name: "Комментарий", Type: "string"},
			{Name: "Звонок", Type: "string"},
			{Name: "Мастер", Type: "string"},
			{Name: "СкрытьМастера", Type: "bool"},
			{Name: "Номер", Type: "string"},
		},
		Forms: []*metadata.FormModule{form},
	}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	anna := &auth.User{ID: "u-anna", Login: "anna", IsAdmin: true}
	post := func(user *auth.User, body url.Values) formEventResponse {
		t.Helper()
		req := httptest.NewRequest("POST", "/ui/processor/"+proc.Name+"/form-event", strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("name", proc.Name)
		ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
		req = req.WithContext(auth.ContextWithUser(ctx, user))
		rec := httptest.NewRecorder()
		srv.handleProcessorFormEvent(rec, req)
		if rec.Code != 200 {
			t.Fatalf("status %d, body=%s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "79161234455") {
			t.Fatalf("номер из параметра без элемента ушёл в браузер: %s", rec.Body.String())
		}
		return decodeFormEventResponse(t, rec.Body.Bytes())
	}

	first := post(anna, processorClickBody("КнопкаУстановить"))
	if !first.OK || first.Error != "" {
		t.Fatalf("первое событие: %+v", first)
	}
	for _, name := range []string{"СкрытьМастера", "Номер"} {
		if _, sent := first.Values[name]; sent {
			t.Errorf("значение %s без элемента отдано в открытом ответе", name)
		}
	}
	if first.Values["Звонок"] != "звонок-17" {
		t.Errorf("нередактируемое поле на форме должно получить значение: %v", first.Values["Звонок"])
	}
	token, _ := first.Values[processorCarryField].(string)
	if token == "" {
		t.Fatalf("нет запечатанного состояния в ответе: %+v", first.Values)
	}
	if first.ElementStates == nil || !first.ElementStates.Hidden["2"] {
		t.Fatalf("hidden_when по флагу не применён: %+v", first.ElementStates)
	}

	// Второе событие флаг не трогает: он приходит из состояния, поле мастера
	// остаётся скрытым (путь элемента «2»), обработчик видит номер и звонок,
	// введённое — как есть.
	second := processorClickBody("КнопкаПроверить")
	second.Set("Комментарий", "введено")
	second.Set(processorCarryField, token)
	got := post(anna, second)
	if want := "true|номер|звонок-17|введено"; len(got.Messages) != 1 || got.Messages[0] != want {
		t.Fatalf("состояние не перенесено: %v, ждали %q", got.Messages, want)
	}
	if got.ElementStates == nil || !got.ElementStates.Hidden["2"] {
		t.Fatalf("hidden_when откатился во втором событии: %+v", got.ElementStates)
	}

	// Подделанный, чужой обработки или чужого пользователя токен не раскрывается.
	for name, tc := range map[string]struct {
		user  *auth.User
		token string
	}{
		"подделка":            {anna, token[:len(token)-4] + "AAAA"},
		"другой пользователь": {&auth.User{ID: "u-bob", Login: "bob", IsAdmin: true}, token},
	} {
		body := processorClickBody("КнопкаПроверить")
		body.Set(processorCarryField, tc.token)
		got := post(tc.user, body)
		if len(got.Messages) != 1 || got.Messages[0] != "false|нет||" {
			t.Errorf("%s: токен раскрыт: %v", name, got.Messages)
		}
	}
}

// Начальная разметка формы обработки несёт то же запечатанное состояние:
// значение параметра без элемента (умолчание) в странице открытым текстом не
// появляется, а первое событие получает его из токена.
func TestProcessorFormCarry_InitialPageSealsUnplacedDefaults(t *testing.T) {
	form := processorExecutionForm(
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ПолеКомментарий", DataPath: "Объект.Комментарий"},
	)
	proc := &processor.Processor{
		Name: "Начало",
		Params: []processor.Param{
			{Name: "Комментарий", Type: "string"},
			{Name: "Секрет", Type: "string", Default: "секрет-по-умолчанию"},
		},
		Forms: []*metadata.FormModule{form},
	}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	user := &auth.User{ID: "u-anna", Login: "anna", IsAdmin: true}
	req := httptest.NewRequest("GET", "/ui/processor/"+proc.Name, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", proc.Name)
	req = req.WithContext(auth.ContextWithUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx), user))
	rec := httptest.NewRecorder()
	srv.processorForm(rec, req)
	page := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(page, `"formAttrs":["_ob_carry"]`) {
		t.Fatalf("нет служебного поля состояния в конфигурации формы: %d", rec.Code)
	}
	if strings.Contains(page, "секрет-по-умолчанию") {
		t.Fatal("значение параметра без элемента попало в разметку открытым текстом")
	}
	start := strings.Index(page, `"formAttrValues":{"_ob_carry":"`)
	if start < 0 {
		t.Fatal("нет начального токена")
	}
	token := page[start+len(`"formAttrValues":{"_ob_carry":"`):]
	token = token[:strings.Index(token, `"`)]
	body := url.Values{processorCarryField: {token}}
	open := httptest.NewRequest("POST", "/", strings.NewReader(body.Encode()))
	open.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	open = open.WithContext(auth.ContextWithUser(open.Context(), user))
	if err := open.ParseForm(); err != nil {
		t.Fatal(err)
	}
	if got := srv.openProcessorCarry(open, proc); got["Секрет"] != "секрет-по-умолчанию" {
		t.Fatalf("начальный токен не раскрылся в умолчания: %v", got)
	}
}
