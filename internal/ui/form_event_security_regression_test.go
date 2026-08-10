package ui

import (
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
	defer postResp.Body.Close()
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
