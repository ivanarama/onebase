package ui

import (
	"context"
	"fmt"
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
	if got := srv.openProcessorCarry(open, proc, form); got["Секрет"] != "секрет-по-умолчанию" {
		t.Fatalf("начальный токен не раскрылся в умолчания: %v", got)
	}
}

// Browser POSTs distinguish an unchecked editable checkbox from a control that
// was not rendered or was disabled. Exercise the carry round trip for both
// form attributes and processor parameters, including helper-name collisions.
func TestProcessorFormCarry_CheckboxPresence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		attribute  bool
		readOnly   bool
		groupRO    bool
		tableChild bool
		unplaced   bool
		marker     bool
		queryOnly  bool
		collision  bool
		want       string
	}{
		{name: "attribute unchecked", attribute: true, marker: true, want: "false"},
		{name: "parameter unchecked", marker: true, want: "false"},
		{name: "attribute absent", attribute: true, want: "true"},
		{name: "attribute query marker", attribute: true, marker: true, queryOnly: true, want: "true"},
		{name: "attribute readonly", attribute: true, readOnly: true, marker: true, want: "true"},
		{name: "attribute readonly group", attribute: true, groupRO: true, marker: true, want: "true"},
		{name: "attribute scalar child in table", attribute: true, tableChild: true, marker: true, want: "true"},
		{name: "attribute unplaced", attribute: true, unplaced: true, marker: true, want: "true"},
		{name: "attribute helper collision", attribute: true, marker: true, collision: true, want: "false"},
		{name: "parameter helper collision", marker: true, collision: true, want: "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkbox := &metadata.FormElement{Kind: metadata.FormElementCheckbox, Name: "FlagInput", DataPath: "Объект.Flag", ReadOnly: tc.readOnly}
			form := processorExecutionForm(
				&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Set", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "SetFlag"}},
				&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Check", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "CheckFlag"}},
			)
			if !tc.unplaced {
				if tc.tableChild {
					form.Elements = append(form.Elements, &metadata.FormElement{Kind: metadata.FormElementTablePart, Name: "Rows", Children: []*metadata.FormElement{checkbox}})
				} else if tc.groupRO {
					form.Elements = append(form.Elements, &metadata.FormElement{Kind: metadata.FormElementGroupBox, ReadOnly: true, Children: []*metadata.FormElement{checkbox}})
				} else {
					form.Elements = append(form.Elements, checkbox)
				}
			}
			form.ProgramAST = mustParse(t, `
Процедура SetFlag()
	Объект.Flag = Истина;
КонецПроцедуры
Процедура CheckFlag()
	Сообщить(Строка(Объект.Flag));
КонецПроцедуры
`)
			proc := &processor.Processor{Name: "CarryCheckbox", Forms: []*metadata.FormModule{form}}
			if tc.attribute {
				form.Attributes = []*metadata.FormAttribute{{Name: "Flag", TypeRef: "bool"}}
			} else {
				proc.Params = []processor.Param{{Name: "Flag", Type: "bool"}}
			}
			if tc.collision {
				proc.Params = append(proc.Params, processor.Param{Name: "_ob_present_Flag", Type: "string"})
			}
			srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
			post := func(body, query url.Values) formEventResponse {
				t.Helper()
				rec := postProcessorFormEventExecutionWithQuery(t, srv, proc.Name, query,
					"application/x-www-form-urlencoded", strings.NewReader(body.Encode()))
				if rec.Code != 200 {
					t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
				}
				resp := decodeFormEventResponse(t, rec.Body.Bytes())
				if !resp.OK || resp.Error != "" {
					t.Fatalf("event failed: %+v", resp)
				}
				return resp
			}
			first := post(processorClickBody("Set"), nil)
			token, _ := first.Values["_ob_carry"].(string)
			if token == "" {
				t.Fatal("missing carry token")
			}
			body := processorClickBody("Check")
			body.Set("_ob_carry", token)
			query := url.Values{}
			if tc.marker {
				marker := "_ob_present_Flag"
				if tc.collision {
					marker += "_"
					body.Set("_ob_present_Flag", "ordinary parameter")
				}
				if tc.queryOnly {
					query.Set(marker, "1")
				} else {
					body.Set(marker, "1")
				}
			}
			second := post(body, query)
			if len(second.Messages) != 1 || second.Messages[0] != tc.want {
				t.Fatalf("checkbox state=%v, want %s", second.Messages, tc.want)
			}
			if tc.unplaced || tc.tableChild {
				if _, exposed := second.Values["Flag"]; exposed {
					t.Fatal("unplaced checkbox value exposed in response")
				}
			} else if second.Values["Flag"] != (tc.want == "true") {
				t.Fatalf("checkbox response=%v, want %s", second.Values["Flag"], tc.want)
			}
			// A third event with no control must restore the newly accepted value.
			body = processorClickBody("Check")
			body.Set("_ob_carry", second.Values["_ob_carry"].(string))
			third := post(body, nil)
			if len(third.Messages) != 1 || third.Messages[0] != tc.want {
				t.Fatalf("new checkbox state lost: %v, want %s", third.Messages, tc.want)
			}
		})
	}
}

func TestProcessorFormCarry_LargeEditableValueRoundTrip(t *testing.T) {
	form := processorExecutionForm(
		&metadata.FormElement{Kind: metadata.FormElementCodeField, Name: "TextInput", DataPath: "Объект.Text"},
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Set", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "SetFlag"}},
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Check", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "CheckFlag"}},
	)
	form.ProgramAST = mustParse(t, `
Процедура SetFlag()
	Объект.Flag = Истина;
КонецПроцедуры
Процедура CheckFlag()
	Сообщить(Строка(Объект.Flag));
КонецПроцедуры
`)
	proc := &processor.Processor{Name: "CarryLarge", Params: []processor.Param{{Name: "Text", Type: "string"}, {Name: "Flag", Type: "bool"}}, Forms: []*metadata.FormModule{form}}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	for _, text := range []string{strings.Repeat("x", 50<<10), strings.Repeat("русский текст\n", 8<<10)} {
		body := processorClickBody("Set")
		body.Set("Text", text)
		var token string
		for event := 0; event < 3; event++ {
			if event > 0 {
				body = processorClickBody("Check")
				body.Set("Text", text)
				body.Set("_ob_carry", token)
			}
			rec := postProcessorFormEventExecution(t, srv, proc.Name, "application/x-www-form-urlencoded", strings.NewReader(body.Encode()))
			if rec.Code != 200 {
				t.Fatalf("event %d: status=%d: %s", event, rec.Code, rec.Body.String())
			}
			resp := decodeFormEventResponse(t, rec.Body.Bytes())
			if !resp.OK || resp.Error != "" || resp.Values["Text"] != text {
				t.Fatalf("event %d: large value not preserved, ok=%v error=%s", event, resp.OK, resp.Error)
			}
			if event > 0 && (len(resp.Messages) != 1 || resp.Messages[0] != "true") {
				t.Fatalf("unplaced flag lost after large editable text: %v", resp.Messages)
			}
			if _, exposed := resp.Values["Flag"]; exposed {
				t.Fatal("unplaced flag exposed in values")
			}
			token, _ = resp.Values["_ob_carry"].(string)
			if len(token) <= 64<<10 {
				t.Fatalf("regression did not cross former 64 KiB token limit: %d", len(token))
			}
		}
	}
}

func TestProcessorFormCarry_OversizedStateReportsError(t *testing.T) {
	form := processorExecutionForm(
		&metadata.FormElement{Kind: metadata.FormElementCodeField, Name: "TextInput", DataPath: "Объект.Text"},
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Check", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Check"}},
	)
	form.ProgramAST = mustParse(t, "Процедура Check()\nКонецПроцедуры")
	proc := &processor.Processor{Name: "CarryOversize", Params: []processor.Param{{Name: "Text", Type: "string"}}, Forms: []*metadata.FormModule{form}}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	body := processorClickBody("Check")
	// The original POST fits the unchanged 1 MiB body limit; its base64 carry
	// would exceed that limit. Return an explicit error and no unusable token.
	body.Set("Text", strings.Repeat("x", 800<<10))
	rec := postProcessorFormEventExecution(t, srv, proc.Name, "application/x-www-form-urlencoded", strings.NewReader(body.Encode()))
	if rec.Code != 200 {
		t.Fatalf("status=%d: request should fit body limit", rec.Code)
	}
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if resp.OK || resp.Error == "" {
		t.Fatalf("oversized carry silently accepted: ok=%v, error=%q", resp.OK, resp.Error)
	}
	if token, _ := resp.Values["_ob_carry"].(string); token != "" {
		t.Fatal("server issued an unusable token")
	}

	// The initial page must also show a visible error for an oversized default,
	// without exposing that unplaced value or issuing a token it cannot read.
	proc.Params = []processor.Param{{Name: "Secret", Type: "string", Default: strings.Repeat("private-default-", 60<<10)}}
	req := httptest.NewRequest("GET", "/ui/processor/"+proc.Name, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", proc.Name)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec = httptest.NewRecorder()
	srv.processorForm(rec, req)
	page := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(page, `class="error"`) {
		t.Fatalf("initial page did not show state-size error: status=%d", rec.Code)
	}
	if strings.Contains(page, "private-default-") {
		t.Fatal("oversized unplaced default exposed in page")
	}
}

// Full browser-encoded POSTs must include BOTH editable controls and the new
// token. Each oversized example fits the inbound limit before adding carry.
func TestProcessorFormCarry_CompletePostBudget(t *testing.T) {
	for _, tc := range []struct {
		name      string
		text      string
		padding   string
		grow      bool
		wantError bool
	}{
		{name: "ASCII fits across events", text: strings.Repeat("x", 400<<10)},
		{name: "ASCII 500 KiB plus token", text: strings.Repeat("x", 500<<10), wantError: true},
		{name: "Cyrillic transport expansion", text: strings.Repeat("я", 140<<10), wantError: true},
		{name: "browser escapes tilde", text: strings.Repeat("~", 250<<10), wantError: true},
		{name: "other POST fields", text: strings.Repeat("x", 250<<10), padding: strings.Repeat("p", 500<<10), wantError: true},
		{name: "handler enlarges text", text: strings.Repeat("x", 250<<10), grow: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form := processorExecutionForm(
				&metadata.FormElement{Kind: metadata.FormElementCodeField, Name: "TextInput", DataPath: "Объект.Text"},
				&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Check", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Check"}},
			)
			program := `
Процедура Check()
	Если Ложь Тогда
		Объект.Text = Объект.Text + Объект.Text;
	КонецЕсли;
	Объект.Flag = Истина;
КонецПроцедуры`
			if tc.grow {
				program = strings.Replace(program, "Ложь", "Истина", 1)
			}
			form.ProgramAST = mustParse(t, program)
			proc := &processor.Processor{Name: "CarryPostBudget", Forms: []*metadata.FormModule{form}, Params: []processor.Param{
				{Name: "Text", Type: "string"}, {Name: "Flag", Type: "bool"},
			}}
			srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
			body := processorClickBody("Check")
			body.Set("Text", tc.text)
			if tc.padding != "" {
				body.Set("other", tc.padding)
			}
			for event := 0; event < 3; event++ {
				// URLSearchParams escapes '~', unlike net/url.Values.Encode.
				encoded := strings.ReplaceAll(body.Encode(), "~", "%7E")
				if len(encoded) > 1<<20 {
					t.Fatalf("event %d: complete POST exceeds 1 MiB: %d", event, len(encoded))
				}
				rec := postProcessorFormEventExecution(t, srv, proc.Name, "application/x-www-form-urlencoded", strings.NewReader(encoded))
				if rec.Code != 200 {
					t.Fatalf("event %d: HTTP %d", event, rec.Code)
				}
				resp := decodeFormEventResponse(t, rec.Body.Bytes())
				token, _ := resp.Values["_ob_carry"].(string)
				if tc.wantError {
					if resp.OK || resp.Error == "" || token != "" {
						t.Fatalf("unusable state issued: ok=%v error=%q tokenBytes=%d", resp.OK, resp.Error, len(token))
					}
					return
				}
				if !resp.OK || resp.Error != "" || token == "" || resp.Values["Text"] != tc.text {
					t.Fatalf("event %d: carry failed: ok=%v error=%q", event, resp.OK, resp.Error)
				}
				if _, exposed := resp.Values["Flag"]; exposed {
					t.Fatal("unplaced flag exposed")
				}
				body.Set("_ob_carry", token)
			}
			// The accepted token still restores the scalar if a later event has
			// no editor (hidden/disabled); the fix must not omit editable state.
			body.Del("Text")
			form.ProgramAST = mustParse(t, `Процедура Check()
	Сообщить(Строка(Объект.Flag) + "|" + Строка(СтрДлина(Объект.Text)));
КонецПроцедуры`)
			rec := postProcessorFormEventExecution(t, srv, proc.Name, "application/x-www-form-urlencoded", strings.NewReader(body.Encode()))
			resp := decodeFormEventResponse(t, rec.Body.Bytes())
			if !resp.OK || len(resp.Messages) != 1 || resp.Messages[0] != "true|409600" {
				t.Fatalf("carried state lost: ok=%v messages=%v", resp.OK, resp.Messages)
			}
		})
	}
}

func TestProcessorFormCarry_InitialPageIncludesEditablePostBudget(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprint("duplicate=", duplicate), func(t *testing.T) {
			form := processorExecutionForm(
				&metadata.FormElement{Kind: metadata.FormElementCodeField, Name: "TextInput", DataPath: "Объект.Text"},
				&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Check", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Check"}},
			)
			form.ProgramAST = mustParse(t, "Процедура Check()\nКонецПроцедуры")
			textBytes := 500 << 10
			if duplicate {
				textBytes = 310 << 10
				form.Elements = append(form.Elements, &metadata.FormElement{Kind: metadata.FormElementCodeField, Name: "TextCopy", DataPath: "Объект.Text"})
			}
			proc := &processor.Processor{Name: "CarryInitialBudget", Forms: []*metadata.FormModule{form}, Params: []processor.Param{
				{Name: "Text", Type: "string", Default: strings.Repeat("x", textBytes)},
			}}
			srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
			req := httptest.NewRequest("GET", "/ui/processor/"+proc.Name, nil)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("name", proc.Name)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			rec := httptest.NewRecorder()
			srv.processorForm(rec, req)
			page := rec.Body.String()
			if rec.Code != 200 || !strings.Contains(page, `class="error"`) || !strings.Contains(page, `"formAttrValues":{"_ob_carry":""}`) {
				t.Fatalf("initial page issued state that cannot fit next POST: HTTP %d", rec.Code)
			}
		})
	}
}

// File forms retain their existing separately configured POST allowance; the
// carry budget must not silently replace it with the small-form body limit.
func TestProcessorFormCarry_FileFormKeepsConfiguredBodyLimit(t *testing.T) {
	form := processorExecutionForm(
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "FileInput", Type: "file", DataPath: "Объект.Data"},
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Check", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Check"}},
	)
	form.ProgramAST = mustParse(t, `Процедура Check()
	Объект.Flag = Истина;
КонецПроцедуры`)
	proc := &processor.Processor{Name: "CarryFileBudget", Forms: []*metadata.FormModule{form}, Params: []processor.Param{
		{Name: "Data", Type: "file"}, {Name: "Flag", Type: "bool"},
	}}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	srv.maxFileSizeBytes = 2 << 20
	body := processorClickBody("Check")
	body.Set("_fc_Data", strings.Repeat("x", 1200<<10))
	for event := 0; event < 2; event++ {
		rec := postProcessorFormEventExecution(t, srv, proc.Name, "application/x-www-form-urlencoded", strings.NewReader(body.Encode()))
		if rec.Code != 200 {
			t.Fatalf("event %d: HTTP %d", event, rec.Code)
		}
		resp := decodeFormEventResponse(t, rec.Body.Bytes())
		token, _ := resp.Values["_ob_carry"].(string)
		if !resp.OK || resp.Error != "" || token == "" {
			t.Fatalf("file form budget rejected: ok=%v error=%q", resp.OK, resp.Error)
		}
		body.Set("_ob_carry", token)
	}
}
