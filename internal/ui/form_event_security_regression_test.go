package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
)

func TestResolveBrowserFormEventFailClosedMatrix(t *testing.T) {
	button := &metadata.FormElement{Kind: metadata.FormElementButton, Name: "Кнопка", Handlers: map[metadata.FormEventType]string{
		metadata.FormEventOnClick: "КнопкаНажатие", metadata.FormEventOnChoice: "КнопкаВыбор",
	}}
	field := &metadata.FormElement{Kind: metadata.FormElementField, Name: "Поле", Handlers: map[metadata.FormEventType]string{
		metadata.FormEventOnChange: "ПолеИзменение", metadata.FormEventOnChoice: "ПолеВыбор",
	}}
	list := &metadata.FormElement{Kind: metadata.FormElementInputList, Name: "Список", Handlers: map[metadata.FormEventType]string{
		metadata.FormEventOnChange: "СписокИзменение", metadata.FormEventStartChoice: "СписокНачало", metadata.FormEventOnChoice: "СписокВыбор",
	}}
	table := &metadata.FormElement{Kind: metadata.FormElementTablePart, Name: "Таблица", Handlers: map[metadata.FormEventType]string{
		metadata.FormEventOnChange: "ТаблицаИзменение", metadata.FormEventOnRowAdded: "ТаблицаДобавление",
		metadata.FormEventOnRowDeleted: "ТаблицаУдаление", metadata.FormEventOnChoice: "ТаблицаВыбор",
	}}
	form := &metadata.FormModule{
		Elements: []*metadata.FormElement{button, field, list, table},
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnOpen: "Открыть"},
	}
	allowed := []struct {
		element string
		event   metadata.FormEventType
	}{
		{"", metadata.FormEventOnOpen},
		{"Кнопка", metadata.FormEventOnClick}, {"Кнопка", metadata.FormEventOnChoice},
		{"Поле", metadata.FormEventOnChange}, {"Поле", metadata.FormEventOnChoice},
		{"Список", metadata.FormEventOnChange}, {"Список", metadata.FormEventStartChoice}, {"Список", metadata.FormEventOnChoice},
		{"Таблица", metadata.FormEventOnChange}, {"Таблица", metadata.FormEventOnRowAdded},
		{"Таблица", metadata.FormEventOnRowDeleted}, {"Таблица", metadata.FormEventOnChoice},
	}
	for _, tc := range allowed {
		if proc, _, fallback, err := resolveBrowserFormEvent(form, tc.element, string(tc.event), false); err != nil || proc == "" || fallback {
			t.Errorf("allowed %q/%q: proc=%q fallback=%v err=%v", tc.element, tc.event, proc, fallback, err)
		}
	}
	rejected := []struct {
		element string
		event   metadata.FormEventType
	}{
		{"", metadata.FormEventBeforeWrite},
		{"Кнопка", metadata.FormEventOnChange},
		{"Поле", metadata.FormEventOnClick},
		{"Список", metadata.FormEventOnClick},
		{"Таблица", metadata.FormEventOnClick},
		{"Нет", metadata.FormEventOnClick},
		{"Кнопка", metadata.FormEventType("Вспомогательная")},
	}
	for _, tc := range rejected {
		if _, _, _, err := resolveBrowserFormEvent(form, tc.element, string(tc.event), false); err == nil {
			t.Errorf("forged %q/%q was accepted", tc.element, tc.event)
		}
	}
}

func TestResolveBrowserFormEventRejectsReadOnlyAndPlacedCommands(t *testing.T) {
	readonlyChild := &metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "КомандаТЧ",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "КомандаТЧНажатие"},
	}
	placedButton := &metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "РазмещённаяКнопка",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "РазмещённоеДействие"},
	}
	form := &metadata.FormModule{
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ReadonlyПоле", ReadOnly: true, Handlers: map[metadata.FormEventType]string{metadata.FormEventOnChange: "Изменить"}},
			{Kind: metadata.FormElementTablePart, Name: "ReadonlyТЧ", ReadOnly: true, Children: []*metadata.FormElement{readonlyChild}},
			placedButton,
		},
		Commands: []*metadata.FormCommand{
			{Name: "РазмещённаяКоманда", Action: "РазмещённоеДействие"},
			{Name: "АвтоКоманда", Action: "АвтоДействие"},
		},
	}
	for _, name := range []string{"ReadonlyПоле", "КомандаТЧ", "РазмещённаяКоманда"} {
		if _, _, _, err := resolveBrowserFormEvent(form, name, string(metadata.FormEventOnClick), false); err == nil {
			t.Errorf("readonly/placed target %q was accepted", name)
		}
	}
	if proc, _, _, err := resolveBrowserFormEvent(form, "АвтоКоманда", string(metadata.FormEventOnClick), false); err != nil || proc != "АвтоДействие" {
		t.Fatalf("unplaced command: proc=%q err=%v", proc, err)
	}
}

func TestResolveBrowserFormEventRejectsAmbiguousTargetNames(t *testing.T) {
	tests := []struct {
		name string
		form *metadata.FormModule
	}{
		{
			name: "duplicate elements",
			form: &metadata.FormModule{Elements: []*metadata.FormElement{
				{Kind: metadata.FormElementButton, Name: "Запустить", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Один"}},
				{Kind: metadata.FormElementButton, Name: "запустить", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Два"}},
			}},
		},
		{
			name: "duplicate commands",
			form: &metadata.FormModule{Commands: []*metadata.FormCommand{
				{Name: "Запустить", Action: "Один"},
				{Name: "ЗАПУСТИТЬ", Action: "Два"},
			}},
		},
		{
			name: "element and command",
			form: &metadata.FormModule{
				Elements: []*metadata.FormElement{{
					Kind: metadata.FormElementButton, Name: "Запустить",
					Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Элемент"},
				}},
				Commands: []*metadata.FormCommand{{Name: "запустить", Action: "Команда"}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, _, err := resolveBrowserFormEvent(test.form, "ЗаПуСтИтЬ", string(metadata.FormEventOnClick), false); err == nil {
				t.Fatal("ambiguous browser target was accepted")
			}
		})
	}
}

func TestManagedFormEffectiveReadOnlyIsSharedByResolverControlsAndRendering(t *testing.T) {
	button := &metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "ВложеннаяКнопка",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Нажать"},
	}
	field := &metadata.FormElement{
		Kind: metadata.FormElementField, Name: "ВложенноеПоле", DataPath: "Объект.Параметр",
	}
	page := &metadata.FormElement{Kind: metadata.FormElementPage, Name: "Страница", Children: []*metadata.FormElement{button, field}}
	group := &metadata.FormElement{Kind: metadata.FormElementGroupBox, Name: "Группа", ReadOnly: true, Children: []*metadata.FormElement{page}}
	form := &metadata.FormModule{
		Name: "Форма", Kind: "custom", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{group},
	}

	if _, _, _, err := resolveBrowserFormEvent(form, button.Name, string(metadata.FormEventOnClick), true); err == nil {
		t.Fatal("button below readonly GroupBox/Page was accepted")
	}
	proc := &processor.Processor{Name: "ReadonlyAncestor", Params: []processor.Param{{Name: "Параметр", Type: "string"}}, Forms: []*metadata.FormModule{form}}
	controls := processorRequestControlsForForm(proc, form)
	if len(controls.paramFields[strings.ToLower("Параметр")]) != 0 {
		t.Fatalf("readonly descendant entered processor allowlist: %+v", controls.paramFields)
	}

	entity := processorVirtualEntity(proc)
	data := map[string]any{
		"Entity": entity, "Processor": proc, "Form": form, "IsProcessor": true,
		"IsNew": true, "Values": map[string]string{}, "RefOptions": map[string]any{},
		"EnumOptions": map[string]any{}, "ChoiceOptions": map[string]any{},
		"TPRefOptions": map[string]any{}, "TPEnumLabels": map[string]map[string]map[string]string{},
		"TPEnumOrder": map[string]map[string][]string{}, "TPRefMeta": map[string]any{},
		"TablePartRows": map[string][]map[string]any{}, "Lang": "ru",
	}
	var rendered bytes.Buffer
	if err := tmpl.ExecuteTemplate(&rendered, "page-managed-form", data); err != nil {
		t.Fatal(err)
	}
	html := rendered.String()
	if strings.Contains(html, `data-ob-fire-click="ВложеннаяКнопка"`) || !strings.Contains(html, `disabled`) {
		t.Fatalf("readonly descendant rendered as fireable: %s", html)
	}
}

func TestHandleManagedFormEventRejectsTablePartValueTableNameCollision(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура Нажать()
	Сообщить("не должно выполняться");
КонецПроцедуры
`, nil, []*metadata.FormElement{{
		Kind: metadata.FormElementButton, Name: "Кнопка",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Нажать"},
	}})
	ent.TableParts = []metadata.TablePart{{Name: "Товары"}}
	ent.Forms[0].Attributes = []*metadata.FormAttribute{{Name: "товары", TypeRef: "ValueTable"}}
	body := url.Values{"_element": {"Кнопка"}, "_event": {string(metadata.FormEventOnClick)}, "_kind": {"object"}}
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
	if resp.OK || !strings.Contains(strings.ToLower(resp.Error), "коллиз") || len(resp.Messages) != 0 {
		t.Fatalf("table namespace collision was accepted: %+v", resp)
	}
}

func TestHandleManagedFormEventUsesOnlyPOSTBrowserState(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура Проверить()
	Если Объект.Наименование = Неопределено Тогда
		Сообщить("поле:нет");
	Иначе
		Сообщить("поле:" + Объект.Наименование);
	КонецЕсли;
	Если ПодборРезультат = Неопределено Тогда
		Сообщить("подбор:нет");
	Иначе
		Сообщить("подбор:есть");
	КонецЕсли;
КонецПроцедуры
`, nil, []*metadata.FormElement{{
		Kind: metadata.FormElementButton, Name: "Проверить",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Проверить"},
	}})

	t.Run("query-only target", func(t *testing.T) {
		query := "?_element=Проверить&_event=" + url.QueryEscape(string(metadata.FormEventOnClick)) + "&_kind=object"
		resp := decodeFormEventResponse(t, executeFormEventWithQuery(t, srv, ent, query, url.Values{}).Body.Bytes())
		if resp.OK || resp.Error == "" || len(resp.Messages) != 0 {
			t.Fatalf("query-only target was accepted: %+v", resp)
		}
	})

	t.Run("query-only id kind header and picker", func(t *testing.T) {
		body := url.Values{"_element": {"Проверить"}, "_event": {string(metadata.FormEventOnClick)}}
		query := url.Values{
			"_id":          {"not-a-uuid"},
			"_kind":        {"missing-kind"},
			"Наименование": {"query-value"},
			"_pick_result": {`[{"id":"forged"}]`},
		}
		resp := decodeFormEventResponse(t, executeFormEventWithQuery(t, srv, ent, "?"+query.Encode(), body).Body.Bytes())
		want := []string{"поле:нет", "подбор:нет"}
		if !resp.OK || resp.Error != "" || strings.Join(resp.Messages, "\x00") != strings.Join(want, "\x00") {
			t.Fatalf("query changed browser state: ok=%v error=%q messages=%v, want=%v", resp.OK, resp.Error, resp.Messages, want)
		}
	})
}

func TestProcessorExecuteFallbackRenderedAndRunsThroughRealHTTP(t *testing.T) {
	form := processorExecutionForm(
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Выполнить"},
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "ДругаяКнопка"},
	)
	proc := &processor.Processor{Name: "HTTPФолбэк", Forms: []*metadata.FormModule{form}}
	program := mustParse(t, `
Процедура Выполнить()
	Сообщить("реальный HTTP");
КонецПроцедуры
`)
	srv, _ := newProcessorFormEventExecutionServer(t, proc, program)
	user := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{
		Processors: map[string][]string{proc.Name: {"run"}},
	}}}}
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), user)))
		})
	})
	router.Get("/ui/processor/{name}", srv.processorForm)
	router.Post("/ui/processor/{name}/form-event", srv.handleProcessorFormEvent)
	httpServer := httptest.NewServer(router)
	defer httpServer.Close()
	endpoint := httpServer.URL + "/ui/processor/" + url.PathEscape(proc.Name)

	getResp, err := http.Get(endpoint) //nolint:gosec // test-only httptest server
	if err != nil {
		t.Fatal(err)
	}
	htmlBytes, readErr := io.ReadAll(getResp.Body)
	_ = getResp.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	html := string(htmlBytes)
	if getResp.StatusCode != http.StatusOK || !strings.Contains(html, `data-ob-fire-click="Выполнить"`) {
		t.Fatalf("fallback button is not actionable: status=%d html=%s", getResp.StatusCode, html)
	}
	if strings.Contains(html, `data-ob-fire-click="ДругаяКнопка"`) {
		t.Fatal("an unrelated unbound button was rendered as executable")
	}

	postResp, err := http.PostForm(endpoint+"/form-event", processorClickBody("Выполнить")) //nolint:gosec // test-only httptest server
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = postResp.Body.Close() }()
	var response formEventResponse
	if err := json.NewDecoder(postResp.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || len(response.Messages) != 1 || response.Messages[0] != "реальный HTTP" {
		t.Fatalf("real HTTP fallback response: ok=%v error=%q messages=%v", response.OK, response.Error, response.Messages)
	}
}

func TestHandleManagedFormEventRejectsForgedHelperProcedure(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура Вспомогательная()
	Сообщить("не должна выполняться");
КонецПроцедуры
`, nil, nil)
	body := url.Values{"_event": {"Вспомогательная"}, "_kind": {"object"}}
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
	if resp.OK || resp.Error == "" || len(resp.Messages) != 0 {
		t.Fatalf("forged helper was not rejected: ok=%v error=%q messages=%v", resp.OK, resp.Error, resp.Messages)
	}
}

func TestHandleManagedFormEventChecksEntityPermissionsSeparately(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура Нажать()
	Сообщить("ok");
КонецПроцедуры
`, nil, []*metadata.FormElement{{
		Kind: metadata.FormElementButton, Name: "Кнопка",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Нажать"},
	}})
	baseBody := url.Values{"_element": {"Кнопка"}, "_event": {string(metadata.FormEventOnClick)}, "_kind": {"object"}}

	request := func(user *auth.User, body url.Values) formEventResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/ui/catalog/"+ent.Name+"/form-event", strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("entity", ent.Name)
		req = req.WithContext(context.WithValue(auth.ContextWithUser(req.Context(), user), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		srv.handleManagedFormEvent(rec, req)
		return decodeFormEventResponse(t, rec.Body.Bytes())
	}
	readUser := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{Catalogs: map[string][]string{ent.Name: {"read"}}}}}}
	writeUser := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{Catalogs: map[string][]string{ent.Name: {"write"}}}}}}
	noAccess := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{}}}}

	if resp := request(noAccess, baseBody); resp.OK || resp.Error == "" {
		t.Fatalf("user without read/write was accepted: %+v", resp)
	}
	if resp := request(readUser, baseBody); resp.OK || resp.Error == "" {
		t.Fatalf("read-only user created a new-record event: %+v", resp)
	}
	if resp := request(writeUser, baseBody); !resp.OK {
		t.Fatalf("write user could not run new-record event: %+v", resp)
	}
	existingID := uuid.New()
	if err := srv.store.Upsert(context.Background(), ent.Name, existingID, map[string]any{"Наименование": "A"}, ent); err != nil {
		t.Fatal(err)
	}
	existingBody := cloneURLValues(baseBody)
	existingBody.Set("_id", existingID.String())
	if resp := request(writeUser, existingBody); resp.OK || resp.Error == "" {
		t.Fatalf("write-only user read an existing-record event: %+v", resp)
	}
	if resp := request(readUser, existingBody); !resp.OK {
		t.Fatalf("read user could not run existing-record event: %+v", resp)
	}
	restrictedUser := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs: map[string][]string{ent.Name: {"read"}},
		RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
			ent.Name: {"read": {Field: "Наименование", Op: "eq", Value: auth.RowValue{Literal: "B"}}},
		}},
	}}}}
	if resp := request(restrictedUser, existingBody); resp.OK || resp.Error == "" {
		t.Fatalf("row-level read restriction was bypassed: %+v", resp)
	}
}

func cloneURLValues(in url.Values) url.Values {
	out := make(url.Values, len(in))
	for key, values := range in {
		out[key] = append([]string(nil), values...)
	}
	return out
}
