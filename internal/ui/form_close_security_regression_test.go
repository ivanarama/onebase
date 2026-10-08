package ui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	runtimepkg "github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

type repeatingCloseBodyReader struct {
	remaining int64
}

func (r *repeatingCloseBodyReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	for i := range p {
		p[i] = 'a'
	}
	r.remaining -= int64(len(p))
	return len(p), nil
}

func deniedCloseUser(login string) *auth.User {
	return &auth.User{ID: login, Login: login, Roles: []*auth.Role{{Permissions: auth.Permission{
		Processors: map[string][]string{},
	}}}}
}

func executeProcessorCloseIntentAsUser(t *testing.T, srv *Server, proc *processor.Processor, body url.Values, user *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	ensureProcessorCloseIntentSchema(proc, body)
	req := httptest.NewRequest(http.MethodPost, "/ui/processor/"+proc.Name+"/form-close-intent", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	setProcessorCloseIntentHeaders(req, proc, body)
	req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	recorder := httptest.NewRecorder()
	router := chi.NewRouter()
	srv.Mount(router)
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestManagedFormCloseIntentNewDiscardSurvivesRevokedWrite(t *testing.T) {
	srv, entity := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Сообщить("HANDLER_MUST_NOT_RUN");
	Объект.Записать();
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	body := closeIntentBody(uuid.NewString(), "close", "unsaved client value")
	recorder := executeFormCloseIntentAsUser(t, srv, entity, body, deniedCloseUser("revoked-new"))
	response := decodeCloseIntentResponse(t, recorder)
	if recorder.Code != http.StatusOK || !response.OK || response.Close == nil ||
		!response.Close.Allowed || !response.Close.Terminal || response.Close.Saved {
		t.Fatalf("revoked new discard did not terminate safely: status=%d response=%+v", recorder.Code, response)
	}
	if response.SavedID != "" || response.SavedLabel != "" || response.Version != 0 || len(response.Messages) != 0 {
		t.Fatalf("terminal response exposed identity or handler state: %+v", response)
	}
	rows, err := srv.store.List(context.Background(), entity.Name, entity, storage.ListParams{})
	if err != nil || len(rows) != 0 {
		t.Fatalf("revoked discard executed handler write: rows=%v err=%v", rows, err)
	}
}

func TestManagedFormCloseIntentUnsavedReplayIsTerminalAfterWriteRevocation(t *testing.T) {
	srv, entity := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Объект.Наименование = "cached secret";
	Сообщить("cached handler message");
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	user := &auth.User{ID: "unsaved-replay", Login: "unsaved-replay", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs: map[string][]string{entity.Name: {"read", "write"}},
	}}}}
	body := closeIntentBody(uuid.NewString(), "close", "browser value")
	first := decodeCloseIntentResponse(t, executeFormCloseIntentAsUser(t, srv, entity, body, user))
	if first.Close == nil || !first.Close.Allowed || first.Close.Terminal || len(first.Messages) != 1 {
		t.Fatalf("initial unsaved close failed: %+v", first)
	}

	user.Roles = []*auth.Role{{Permissions: auth.Permission{
		Catalogs: map[string][]string{entity.Name: {"read"}},
	}}}
	recorder := executeFormCloseIntentAsUser(t, srv, entity, body, user)
	replay := decodeCloseIntentResponse(t, recorder)
	if recorder.Code != http.StatusOK || replay.Close == nil || !replay.Close.Allowed || !replay.Close.Terminal {
		t.Fatalf("revoked unsaved replay was not terminal: status=%d response=%+v", recorder.Code, replay)
	}
	if replay.SavedID != "" || replay.Version != 0 || len(replay.Values) != 0 || len(replay.Messages) != 0 ||
		strings.Contains(recorder.Body.String(), "cached secret") || strings.Contains(recorder.Body.String(), "cached handler") {
		t.Fatalf("revoked unsaved replay exposed cached state: %+v", replay)
	}
}

func TestManagedFormCloseIntentExistingFormSurvivesFullyRevokedAccess(t *testing.T) {
	srv, entity := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Сообщить("HANDLER_MUST_NOT_RUN");
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	id := uuid.New()
	if err := srv.store.Upsert(context.Background(), entity.Name, id, map[string]any{"Наименование": "secret"}, entity); err != nil {
		t.Fatal(err)
	}
	body := closeIntentBody(uuid.NewString(), "close", "stale browser value")
	body.Set("_id", id.String())
	body.Set("_version", "1")
	recorder := executeFormCloseIntentAsUser(t, srv, entity, body, deniedCloseUser("revoked-existing"))
	response := decodeCloseIntentResponse(t, recorder)
	if recorder.Code != http.StatusOK || !response.OK || response.Close == nil ||
		!response.Close.Allowed || !response.Close.Terminal || response.Close.Saved {
		t.Fatalf("fully revoked existing form did not terminate safely: status=%d response=%+v", recorder.Code, response)
	}
	if response.SavedID != "" || response.SavedLabel != "" || response.Version != 0 ||
		len(response.Values) != 0 || len(response.Messages) != 0 {
		t.Fatalf("terminal response exposed durable state: %+v", response)
	}
}

func TestManagedFormCloseIntentReplayIsSanitizedAfterAccessRevocation(t *testing.T) {
	srv, entity := setupManagedEventsServer(t, "", nil, nil)
	id := uuid.New()
	if err := srv.store.Upsert(context.Background(), entity.Name, id, map[string]any{"Наименование": "visible first"}, entity); err != nil {
		t.Fatal(err)
	}
	user := &auth.User{ID: "same-user", Login: "same-user", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs: map[string][]string{entity.Name: {"read", "write"}},
	}}}}
	body := closeIntentBody(uuid.NewString(), "close", "visible first")
	body.Set("_id", id.String())
	body.Set("_version", "1")
	first := decodeCloseIntentResponse(t, executeFormCloseIntentAsUser(t, srv, entity, body, user))
	if first.Close == nil || !first.Close.Allowed || first.Close.Terminal {
		t.Fatalf("initial authorized close failed: %+v", first)
	}

	user.Roles = []*auth.Role{{Permissions: auth.Permission{}}}
	replayRecorder := executeFormCloseIntentAsUser(t, srv, entity, body, user)
	replay := decodeCloseIntentResponse(t, replayRecorder)
	if replayRecorder.Code != http.StatusOK || replay.Close == nil || !replay.Close.Allowed || !replay.Close.Terminal {
		t.Fatalf("revoked exact replay was not converted to terminal: status=%d response=%+v", replayRecorder.Code, replay)
	}
	if replay.Close.Saved || replay.SavedID != "" || replay.SavedLabel != "" || replay.Version != 0 ||
		len(replay.Values) != 0 || len(replay.Messages) != 0 {
		t.Fatalf("revoked replay exposed cached state: %+v", replay)
	}
}

func TestManagedFormCloseIntentReplayDropsPayloadAfterFieldPolicyChange(t *testing.T) {
	srv, entity := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Объект.Наименование = "TOP SECRET VALUE";
	Сообщить("TOP SECRET MESSAGE");
	Отказ = Истина;
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	id := uuid.New()
	if err := srv.store.Upsert(context.Background(), entity.Name, id, map[string]any{"Наименование": "before"}, entity); err != nil {
		t.Fatal(err)
	}
	user := &auth.User{ID: "field-policy-replay", Login: "field-policy-replay", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs: map[string][]string{entity.Name: {"read", "write"}},
	}}}}
	body := closeIntentBody(uuid.NewString(), "close", "before")
	body.Set("_id", id.String())
	body.Set("_version", "1")
	firstRecorder := executeFormCloseIntentAsUser(t, srv, entity, body, user)
	first := decodeCloseIntentResponse(t, firstRecorder)
	if first.Close == nil || first.Close.Allowed || len(first.Messages) != 1 ||
		!strings.Contains(firstRecorder.Body.String(), "TOP SECRET") {
		t.Fatalf("initial response does not exercise sensitive cached payload: %+v", first)
	}

	user.Roles = []*auth.Role{{Permissions: auth.Permission{
		Catalogs: map[string][]string{entity.Name: {"read", "write"}},
		FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{
			entity.Name: {"Наименование": {Read: "mask_all"}},
		}},
	}}}
	replayRecorder := executeFormCloseIntentAsUser(t, srv, entity, body, user)
	replay := decodeCloseIntentResponse(t, replayRecorder)
	if replayRecorder.Code != http.StatusConflict || replay.Close == nil || replay.Close.Allowed || replay.OK {
		t.Fatalf("policy-changed replay did not fail closed: status=%d response=%+v", replayRecorder.Code, replay)
	}
	if len(replay.Values) != 0 || len(replay.Messages) != 0 || len(replay.RefOptions) != 0 ||
		strings.Contains(replayRecorder.Body.String(), "TOP SECRET") {
		t.Fatalf("policy-changed replay exposed cached payload: %s", replayRecorder.Body.String())
	}
}

func TestManagedFormCloseIntentDocumentAlternateWrapperReturnsExactVersion(t *testing.T) {
	document := &metadata.Entity{
		Name: "CloseDocumentAlternateWrapper", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Комментарий", Type: metadata.FieldTypeString}},
	}
	document.Forms = []*metadata.FormModule{{
		Name: "Object", Kind: "object", EntityName: document.Name, LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "Comment", DataPath: "Объект.Комментарий"}},
		Handlers: map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"},
		ProgramAST: mustParse(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Другой = Объект.Ссылка.ПолучитьОбъект();
	Другой.Комментарий = "written through document wrapper";
	Другой.Записать();
	Отказ = Истина;
КонецПроцедуры
`),
	}}
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{document})
	body := closeIntentBody(uuid.NewString(), "ok", "")
	body.Del("Наименование")
	body.Set("Комментарий", "initial close save")
	body.Set("_close_mode", "save")
	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, document, body))
	if response.Close == nil || response.Close.Allowed || !response.Close.Saved || response.Version != 2 {
		t.Fatalf("document wrapper write lost exact committed version: %+v", response)
	}
	if got := response.Values["Комментарий"]; got != "written through document wrapper" {
		t.Fatalf("document wrapper state was not adopted: got=%v response=%+v", got, response)
	}
	id, err := uuid.Parse(response.SavedID)
	if err != nil {
		t.Fatalf("saved id: %v", err)
	}
	row, err := srv.store.GetByID(ctx, document.Name, id, document)
	if err != nil || row["Комментарий"] != "written through document wrapper" || row["_version"] != int64(2) {
		t.Fatalf("document wrapper durable state mismatch: row=%v err=%v", row, err)
	}
}

func TestManagedFormCloseIntentAlternateWrapperAdoptsExactTableParts(t *testing.T) {
	fixture := setupFormCtxServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Другой = Объект.Ссылка.ПолучитьОбъект();
	Другой.Строки.Очистить();
	Строка = Другой.Строки.Добавить();
	Строка.Канал = "durable wrapper row";
	Другой.Записать();
	Отказ = Истина;
КонецПроцедуры
`, nil)
	fixture.entity.Forms[0].Handlers = map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}
	id := uuid.New()
	ctx := context.Background()
	if err := fixture.srv.store.Upsert(ctx, fixture.entity.Name, id, map[string]any{
		"Наименование": "existing", "Канал": "header",
	}, fixture.entity); err != nil {
		t.Fatal(err)
	}
	if err := fixture.srv.store.UpsertTablePartRows(ctx, fixture.entity.Name, "Строки", id,
		[]map[string]any{{"Канал": "browser baseline"}}, fixture.entity.TableParts[0]); err != nil {
		t.Fatal(err)
	}
	body := closeIntentBody(uuid.NewString(), "close", "existing")
	body.Set("_id", id.String())
	body.Set("_version", "1")
	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, fixture.srv, fixture.entity, body))
	if response.Close == nil || response.Close.Allowed || !response.Close.Saved || response.Version != 2 {
		t.Fatalf("alternate wrapper table write lost exact version: %+v", response)
	}
	rows := response.TableParts["Строки"]
	if len(rows) != 1 || rows[0]["Канал"] != "durable wrapper row" {
		t.Fatalf("response paired v2 with stale table parts: %v", rows)
	}
	durable, err := fixture.srv.store.GetTablePartRows(ctx, fixture.entity.Name, "Строки", id, fixture.entity.TableParts[0])
	if err != nil || len(durable) != 1 || durable[0]["Канал"] != "durable wrapper row" {
		t.Fatalf("durable wrapper table state mismatch: rows=%v err=%v", durable, err)
	}
}

func TestManagedFormCloseIntentDoesNotPublishConcurrentExternalVersion(t *testing.T) {
	srv, entity := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Другой = Объект.Ссылка.ПолучитьОбъект();
	Другой.Наименование = "own committed v2";
	Другой.Записать();
	Sleep(0.4);
	Отказ = Истина;
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	ctx := context.Background()
	id := uuid.New()
	if err := srv.store.Upsert(ctx, entity.Name, id, map[string]any{"Наименование": "v1"}, entity); err != nil {
		t.Fatal(err)
	}
	body := closeIntentBody(uuid.NewString(), "close", "v1")
	body.Set("_id", id.String())
	body.Set("_version", "1")
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- executeFormCloseIntent(t, srv, entity, body) }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		version, err := srv.store.EntityVersion(ctx, entity.Name, id)
		if err == nil && version == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("alternate wrapper did not publish v2 before pause: version=%d err=%v", version, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	expectedV2 := int64(2)
	if err := srv.store.UpsertVersioned(ctx, entity.Name, id,
		map[string]any{"Наименование": "external committed v3"}, entity, &expectedV2); err != nil {
		t.Fatalf("external v3 write: %v", err)
	}

	var recorder *httptest.ResponseRecorder
	select {
	case recorder = <-result:
	case <-time.After(3 * time.Second):
		t.Fatal("close request did not finish after concurrent write")
	}
	response := decodeCloseIntentResponse(t, recorder)
	if response.Close == nil || response.Close.Allowed || !response.Close.Saved || response.Version != 2 {
		t.Fatalf("close response promoted a version it did not write: %+v", response)
	}
	if got := response.Values["Наименование"]; got == "external committed v3" {
		t.Fatalf("close response mixed external v3 into its v2 outcome: %+v", response)
	}
	row, err := srv.store.GetByID(ctx, entity.Name, id, entity)
	if err != nil || row["Наименование"] != "external committed v3" || row["_version"] != int64(3) {
		t.Fatalf("external v3 was not durable: row=%v err=%v", row, err)
	}

	retry := closeIntentBody(uuid.NewString(), "ok", "stale v2 overwrite")
	retry.Set("_id", id.String())
	retry.Set("_version", "2")
	retry.Set("_close_mode", "save")
	conflictRecorder := executeFormCloseIntent(t, srv, entity, retry)
	if conflictRecorder.Code != http.StatusConflict {
		t.Fatalf("next save with returned v2 did not conflict with external v3: status=%d body=%s",
			conflictRecorder.Code, conflictRecorder.Body.String())
	}
	row, err = srv.store.GetByID(ctx, entity.Name, id, entity)
	if err != nil || row["Наименование"] != "external committed v3" || row["_version"] != int64(3) {
		t.Fatalf("stale retry overwrote external v3: row=%v err=%v", row, err)
	}
}

func TestManagedFormCloseIntentManagerUnpostPublishesCanonicalState(t *testing.T) {
	document := &metadata.Entity{
		Name: "CloseManagerUnpost", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	document.Forms = []*metadata.FormModule{{
		Name: "Object", Kind: "object", EntityName: document.Name, LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "Number", DataPath: "Объект.Номер"}},
		Handlers: map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"},
		ProgramAST: mustParse(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Документы.CloseManagerUnpost.ОтменитьПроведение(Объект.Ссылка);
	Отказ = Истина;
КонецПроцедуры
`),
	}}
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{document})
	id := uuid.New()
	if err := srv.store.Upsert(ctx, document.Name, id, map[string]any{"Номер": "D-1"}, document); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetPosted(ctx, document.Name, id, true); err != nil {
		t.Fatal(err)
	}
	body := closeIntentBody(uuid.NewString(), "close", "")
	body.Del("Наименование")
	body.Set("Номер", "D-1")
	body.Set("_id", id.String())
	body.Set("_version", "1")
	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, document, body))
	if response.Close == nil || response.Close.Allowed || !response.Close.Saved || response.Version != 1 {
		t.Fatalf("manager unpost did not publish exact durable outcome: %+v", response)
	}
	if posted, present := response.Values["posted"]; !present || asBool(posted) {
		t.Fatalf("response did not publish posted=false delta: values=%v", response.Values)
	}
	row, err := srv.store.GetByID(ctx, document.Name, id, document)
	if err != nil || asBool(row["posted"]) {
		t.Fatalf("manager unpost did not persist: row=%v err=%v", row, err)
	}
}

func TestManagedFormCloseIntentProtocolHeadersDoNotOverwriteEntityFields(t *testing.T) {
	entity := &metadata.Entity{
		Name: "CloseProtocolCollision", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "_close_intent_id", Type: metadata.FieldTypeString},
			{Name: "_close_mode", Type: metadata.FieldTypeString},
		},
	}
	entity.Forms = []*metadata.FormModule{{
		Name: "Object", Kind: "object", EntityName: entity.Name, LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "UserIntent", DataPath: "Объект._close_intent_id"},
			{Kind: metadata.FormElementField, Name: "UserMode", DataPath: "Объект._close_mode"},
		},
	}}
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
	envelope := closeIntentBody(uuid.NewString(), "ok", "")
	envelope.Set("_close_mode", "save")
	ensureEntityCloseIntentSchema(entity, envelope)
	formBody := url.Values{
		"_kind":            {"object"},
		"_close_intent_id": {"ordinary user intent value"},
		"_close_mode":      {"ordinary user mode value"},
	}
	req := httptest.NewRequest(http.MethodPost,
		"/ui/catalog/"+entity.Name+"/form-close-intent", strings.NewReader(formBody.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	setEntityCloseIntentHeaders(req, envelope)
	recorder := httptest.NewRecorder()
	router := chi.NewRouter()
	srv.Mount(router)
	router.ServeHTTP(recorder, req)
	response := decodeCloseIntentResponse(t, recorder)
	if recorder.Code != http.StatusOK || response.Close == nil || !response.Close.Allowed || !response.Close.Saved {
		t.Fatalf("collision close-save failed: status=%d response=%+v", recorder.Code, response)
	}
	id, err := uuid.Parse(response.SavedID)
	if err != nil {
		t.Fatalf("invalid saved id %q: %v", response.SavedID, err)
	}
	row, err := srv.store.GetByID(ctx, entity.Name, id, entity)
	if err != nil {
		t.Fatal(err)
	}
	if row["_close_intent_id"] != "ordinary user intent value" || row["_close_mode"] != "ordinary user mode value" {
		t.Fatalf("protocol envelope overwrote entity fields: row=%v", row)
	}
}

func TestProcessorFormCloseIntentSurvivesRevokedRunPermission(t *testing.T) {
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementField, Name: "Name", DataPath: "Объект.Имя",
	})
	form.Handlers = map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}
	form.ProgramAST = mustParse(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Сообщить("HANDLER_MUST_NOT_RUN_AFTER_REVOKE");
КонецПроцедуры
`)
	proc := &processor.Processor{
		Name: "CloseRevokedProcessor", Params: []processor.Param{{Name: "Имя", Type: "string"}},
		Forms: []*metadata.FormModule{form},
	}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	body := processorCloseIntentBody(proc, uuid.NewString())
	body.Set("Имя", "unsaved processor value")
	user := deniedCloseUser("revoked-processor")
	recorder := executeProcessorCloseIntentAsUser(t, srv, proc, body, user)
	response := decodeCloseIntentResponse(t, recorder)
	if recorder.Code != http.StatusOK || !response.OK || response.Close == nil ||
		!response.Close.Allowed || !response.Close.Terminal || response.Close.IntentID == "" {
		t.Fatalf("revoked processor close was not correlated terminal: status=%d response=%+v", recorder.Code, response)
	}
	if len(response.Values) != 0 || len(response.Messages) != 0 || response.Error != "" {
		t.Fatalf("revoked processor close executed/leaked handler state: %+v", response)
	}
}

func TestProcessorFormCloseIntentReplayIsSanitizedAfterRunRevocation(t *testing.T) {
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementField, Name: "Name", DataPath: "Объект.Имя",
	})
	form.Handlers = map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}
	form.ProgramAST = mustParse(t, `
Процедура ПроверитьЗакрытие()
	Сообщить("visible only while authorized");
КонецПроцедуры
`)
	proc := &processor.Processor{
		Name: "CloseRevokedProcessorReplay", Params: []processor.Param{{Name: "Имя", Type: "string"}},
		Forms: []*metadata.FormModule{form},
	}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	user := &auth.User{ID: "processor-user", Login: "processor-user", Roles: []*auth.Role{{Permissions: auth.Permission{
		Processors: map[string][]string{proc.Name: {"run"}},
	}}}}
	body := processorCloseIntentBody(proc, uuid.NewString())
	body.Set("Имя", "browser state")
	first := decodeCloseIntentResponse(t, executeProcessorCloseIntentAsUser(t, srv, proc, body, user))
	if first.Close == nil || !first.Close.Allowed || first.Close.Terminal || len(first.Messages) != 1 {
		t.Fatalf("initial authorized processor close failed: %+v", first)
	}

	user.Roles = []*auth.Role{{Permissions: auth.Permission{Processors: map[string][]string{}}}}
	replayRecorder := executeProcessorCloseIntentAsUser(t, srv, proc, body, user)
	replay := decodeCloseIntentResponse(t, replayRecorder)
	if replayRecorder.Code != http.StatusOK || replay.Close == nil || !replay.Close.Allowed || !replay.Close.Terminal {
		t.Fatalf("revoked processor replay was not terminal: status=%d response=%+v", replayRecorder.Code, replay)
	}
	if len(replay.Values) != 0 || len(replay.Messages) != 0 || replay.Error != "" {
		t.Fatalf("revoked processor replay exposed cached state: %+v", replay)
	}
}

func TestProcessorFormCloseIntentIsTerminalAfterProcessorHotRemoval(t *testing.T) {
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementField, Name: "Name", DataPath: "Объект.Имя",
	})
	proc := &processor.Processor{
		Name: "CloseRemovedProcessor", Params: []processor.Param{
			{Name: "Имя", Type: "string"},
			{Name: "_close_intent_id", Type: "string"},
			{Name: "_ob_service_close_intent_id", Type: "string"},
		},
		Forms: []*metadata.FormModule{form},
	}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	intentID := uuid.NewString()
	body := processorCloseIntentBody(proc, intentID)
	body.Set("_close_intent_id", "ordinary user parameter, not lifecycle UUID")
	body.Set("_ob_service_close_intent_id", "another colliding user parameter")
	body.Set("Имя", "client-only state")
	srv.reg.LoadProcessors(nil)

	recorder := executeProcessorCloseIntent(t, srv, proc, body)
	response := decodeCloseIntentResponse(t, recorder)
	if recorder.Code != http.StatusOK || response.Close == nil || !response.Close.Allowed ||
		!response.Close.Terminal || response.Close.IntentID != intentID {
		t.Fatalf("removed processor did not produce correlated terminal close: status=%d response=%+v", recorder.Code, response)
	}
	if len(response.Values) != 0 || len(response.Messages) != 0 || response.SavedID != "" || response.Version != 0 {
		t.Fatalf("removed processor terminal leaked stale state: %+v", response)
	}
}

func TestManagedFormCloseIntentIsTerminalAfterFormHotRemoval(t *testing.T) {
	srv, entity := setupManagedEventsServer(t, "", nil, nil)
	body := closeIntentBody(uuid.NewString(), "close", "client-only state")
	entity.Forms = nil

	recorder := executeFormCloseIntent(t, srv, entity, body)
	response := decodeCloseIntentResponse(t, recorder)
	if recorder.Code != http.StatusOK || response.Close == nil || !response.Close.Allowed ||
		!response.Close.Terminal || response.Close.IntentID == "" {
		t.Fatalf("removed managed form did not produce correlated terminal close: status=%d response=%+v", recorder.Code, response)
	}
	if len(response.Values) != 0 || len(response.Messages) != 0 || response.SavedID != "" || response.Version != 0 {
		t.Fatalf("removed form terminal leaked stale state: %+v", response)
	}
}

func TestManagedFormCloseIntentFreshWriteReconcilesAfterEntityHotRemoval(t *testing.T) {
	for _, mode := range []string{"save", "post", "save_and_select"} {
		t.Run(mode, func(t *testing.T) {
			srv, entity := setupManagedEventsServer(t, "", nil, nil)
			// Capture the lifecycle envelope while the entity/form still exists,
			// exactly as a browser-rendered form does before a hot reload.
			body := closeIntentBody(uuid.NewString(), "ok", "must not persist")
			body.Set("_close_mode", mode)
			ensureEntityCloseIntentSchema(entity, body)
			srv.reg.ReplaceProjectFrom(runtimepkg.NewRegistry())

			recorder := executeFormCloseIntent(t, srv, entity, body)
			response := decodeCloseIntentResponse(t, recorder)
			if recorder.Code != http.StatusConflict || response.Close == nil ||
				response.Close.Allowed || response.Close.Terminal || !response.Close.Reconcile ||
				response.Close.Saved || response.SavedID != "" || response.Version != 0 {
				t.Fatalf("fresh %s after entity removal did not reconcile safely: status=%d response=%+v",
					mode, recorder.Code, response)
			}
			rows, err := srv.store.List(context.Background(), entity.Name, entity, storage.ListParams{})
			if err != nil || len(rows) != 0 {
				t.Fatalf("fresh %s after entity removal changed the database: rows=%v err=%v", mode, rows, err)
			}
		})
	}
}

func TestManagedFormCloseIntentTerminalDiscardReplaySurvivesEntityHotRemoval(t *testing.T) {
	srv, entity := setupManagedEventsServer(t, "", nil, nil)
	body := closeIntentBody(uuid.NewString(), "close", "client-only state")
	ensureEntityCloseIntentSchema(entity, body)
	srv.reg.ReplaceProjectFrom(runtimepkg.NewRegistry())

	firstRecorder := executeFormCloseIntent(t, srv, entity, body)
	first := decodeCloseIntentResponse(t, firstRecorder)
	if firstRecorder.Code != http.StatusOK || first.Close == nil ||
		!first.Close.Allowed || !first.Close.Terminal || first.Close.Reconcile {
		t.Fatalf("fresh discard after entity removal was not terminal: status=%d response=%+v", firstRecorder.Code, first)
	}

	replayRecorder := executeFormCloseIntent(t, srv, entity, body)
	replay := decodeCloseIntentResponse(t, replayRecorder)
	if replayRecorder.Code != http.StatusOK || replay.Close == nil ||
		!replay.Close.Allowed || !replay.Close.Terminal || replay.Close.Reconcile ||
		replay.Close.Saved || replay.SavedID != "" || replay.Version != 0 {
		t.Fatalf("retained terminal discard was rewritten on exact replay: status=%d response=%+v", replayRecorder.Code, replay)
	}
	if len(replay.Values) != 0 || len(replay.TableParts) != 0 || len(replay.Messages) != 0 {
		t.Fatalf("retained terminal discard replay exposed stale state: %+v", replay)
	}
}

func TestFormCloseIntentExactReplaySurvivesMetadataRemoval(t *testing.T) {
	t.Run("entity", func(t *testing.T) {
		srv, entity := setupManagedEventsServer(t, "", nil, nil)
		body := closeIntentBody(uuid.NewString(), "ok", "client state")
		body.Set("_close_mode", "save")
		first := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, entity, body))
		if first.Close == nil || !first.Close.Allowed || !first.Close.Saved || first.SavedID == "" {
			t.Fatalf("initial entity close failed: %+v", first)
		}
		srv.reg.ReplaceProjectFrom(runtimepkg.NewRegistry())

		recorder := executeFormCloseIntent(t, srv, entity, body)
		replay := decodeCloseIntentResponse(t, recorder)
		if recorder.Code != http.StatusConflict || replay.Close == nil || replay.Close.Allowed ||
			replay.Close.Terminal || !replay.Close.Reconcile || replay.Close.Saved || replay.SavedID != "" || replay.Version != 0 ||
			replay.Close.IntentID != body.Get("_close_intent_id") {
			t.Fatalf("entity metadata removal did not redact exact replay: status=%d response=%+v", recorder.Code, replay)
		}
		if len(replay.Values) != 0 || len(replay.TableParts) != 0 || len(replay.Messages) != 0 {
			t.Fatalf("entity metadata removal replay exposed cached state: %+v", replay)
		}
	})

	t.Run("processor", func(t *testing.T) {
		form := processorExecutionForm()
		proc := &processor.Processor{Name: "CloseReplayRemovedProcessor", Forms: []*metadata.FormModule{form}}
		srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
		body := processorCloseIntentBody(proc, uuid.NewString())
		first := decodeCloseIntentResponse(t, executeProcessorCloseIntent(t, srv, proc, body))
		if first.Close == nil || !first.Close.Allowed {
			t.Fatalf("initial processor close failed: %+v", first)
		}
		srv.reg.LoadProcessors(nil)

		recorder := executeProcessorCloseIntent(t, srv, proc, body)
		replay := decodeCloseIntentResponse(t, recorder)
		if recorder.Code != http.StatusOK || replay.Close == nil || !replay.Close.Allowed ||
			replay.Close.IntentID != body.Get(processorServiceFieldName(proc.Params, "_close_intent_id")) {
			t.Fatalf("processor metadata removal broke exact replay: status=%d response=%+v", recorder.Code, replay)
		}
	})
}

func TestMissingMetadataCloseDoesNotReadFormerlyValidLargeBody(t *testing.T) {
	t.Run("entity richtext", func(t *testing.T) {
		srv, entity := setupManagedEventsServer(t, "", nil, nil)
		for i := 0; i < 5; i++ {
			entity.Fields = append(entity.Fields, metadata.Field{Name: "Rich" + strconv.Itoa(i), Type: metadata.FieldTypeRichText})
		}
		bodySize := srv.effectiveUploadLimit() + uiMultipartOverhead + 1
		limitRequest := httptest.NewRequest(http.MethodPost, "/", nil)
		limitRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		if limit := srv.entityFormBodyLimit(limitRequest, entity); limit <= bodySize {
			t.Fatalf("test body is not valid under original entity metadata: limit=%d size=%d", limit, bodySize)
		}
		envelope := closeIntentBody(uuid.NewString(), "close", "")
		ensureEntityCloseIntentSchema(entity, envelope)
		srv.reg.ReplaceProjectFrom(runtimepkg.NewRegistry())
		req := httptest.NewRequest(http.MethodPost,
			"/ui/"+strings.ToLower(string(entity.Kind))+"/"+entity.Name+"/form-close-intent",
			&repeatingCloseBodyReader{remaining: bodySize})
		req.ContentLength = bodySize
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		setEntityCloseIntentHeaders(req, envelope)
		recorder := httptest.NewRecorder()
		router := chi.NewRouter()
		srv.Mount(router)
		router.ServeHTTP(recorder, req)
		response := decodeCloseIntentResponse(t, recorder)
		if recorder.Code != http.StatusOK || response.Close == nil || !response.Close.Allowed || !response.Close.Terminal {
			t.Fatalf("large stale entity form was not correlated: status=%d response=%+v", recorder.Code, response)
		}
	})

	t.Run("processor file", func(t *testing.T) {
		form := processorExecutionForm(&metadata.FormElement{
			Kind: metadata.FormElementField, Name: "File", DataPath: "Объект.File", Type: "file",
		})
		proc := &processor.Processor{
			Name: "CloseRemovedLargeProcessor", Params: []processor.Param{{Name: "File", Type: "file"}},
			Forms: []*metadata.FormModule{form},
		}
		srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
		bodySize := srv.effectiveUploadLimit() + uiMultipartOverhead + 1
		limitRequest := httptest.NewRequest(http.MethodPost, "/", nil)
		limitRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		controls := processorRequestControlsForForm(proc, form)
		if limit := processorFormBodyLimit(limitRequest, srv.effectiveUploadLimit(), controls); limit <= bodySize {
			t.Fatalf("test body is not valid under original processor metadata: limit=%d size=%d", limit, bodySize)
		}
		envelope := processorCloseIntentBody(proc, uuid.NewString())
		ensureProcessorCloseIntentSchema(proc, envelope)
		srv.reg.LoadProcessors(nil)
		req := httptest.NewRequest(http.MethodPost, "/ui/processor/"+proc.Name+"/form-close-intent",
			&repeatingCloseBodyReader{remaining: bodySize})
		req.ContentLength = bodySize
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		setProcessorCloseIntentHeaders(req, proc, envelope)
		recorder := httptest.NewRecorder()
		router := chi.NewRouter()
		srv.Mount(router)
		router.ServeHTTP(recorder, req)
		response := decodeCloseIntentResponse(t, recorder)
		if recorder.Code != http.StatusOK || response.Close == nil || !response.Close.Allowed || !response.Close.Terminal {
			t.Fatalf("large stale processor form was not correlated: status=%d response=%+v", recorder.Code, response)
		}
	})
}

func TestExactReplayPrecedesShrunkMetadataBodyLimit(t *testing.T) {
	t.Run("entity richtext removed", func(t *testing.T) {
		srv, entity := setupManagedEventsServer(t, "", nil, nil)
		entity.Fields = append(entity.Fields, metadata.Field{Name: "Rich", Type: metadata.FieldTypeRichText})
		entity.Forms[0].Elements = append(entity.Forms[0].Elements, &metadata.FormElement{
			Kind: metadata.FormElementField, Name: "Rich", DataPath: "Объект.Rich",
		})
		body := closeIntentBody(uuid.NewString(), "cross", "client state")
		body.Set("Rich", strings.Repeat("a", 2<<20))
		first := executeFormCloseIntent(t, srv, entity, body)
		if first.Code != http.StatusOK {
			t.Fatalf("old richtext form close failed: status=%d body=%s", first.Code, first.Body.String())
		}
		entity.Fields = entity.Fields[:len(entity.Fields)-1]
		entity.Forms[0].Elements = entity.Forms[0].Elements[:len(entity.Forms[0].Elements)-1]

		replayRequest := httptest.NewRequest(http.MethodPost,
			"/ui/"+strings.ToLower(string(entity.Kind))+"/"+entity.Name+"/form-close-intent",
			strings.NewReader(body.Encode()))
		replayRequest.ContentLength = -1
		replayRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		setEntityCloseIntentHeaders(replayRequest, body)
		replayRecorder := httptest.NewRecorder()
		replayRouter := chi.NewRouter()
		srv.Mount(replayRouter)
		replayRouter.ServeHTTP(replayRecorder, replayRequest)
		replay := decodeCloseIntentResponse(t, replayRecorder)
		if replayRecorder.Code != http.StatusOK || replay.Close == nil || !replay.Close.Allowed {
			t.Fatalf("shrunk entity metadata rejected exact replay: status=%d response=%+v", replayRecorder.Code, replay)
		}
	})

	t.Run("processor file removed", func(t *testing.T) {
		form := processorExecutionForm(&metadata.FormElement{
			Kind: metadata.FormElementField, Name: "File", DataPath: "Объект.File", Type: "file",
		})
		proc := &processor.Processor{
			Name: "CloseShrunkProcessor", Params: []processor.Param{{Name: "File", Type: "file"}},
			Forms: []*metadata.FormModule{form},
		}
		srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
		srv.maxFileSizeBytes = 1 << 20
		body := processorCloseIntentBody(proc, uuid.NewString())
		body.Set("File", strings.Repeat("a", 1<<20))
		first := executeProcessorCloseIntent(t, srv, proc, body)
		if first.Code != http.StatusOK {
			t.Fatalf("old processor file form close failed: status=%d body=%s", first.Code, first.Body.String())
		}
		proc.Params = nil
		form.Elements = nil

		replayRequest := httptest.NewRequest(http.MethodPost, "/ui/processor/"+proc.Name+"/form-close-intent",
			strings.NewReader(body.Encode()))
		replayRequest.ContentLength = -1
		replayRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		setProcessorCloseIntentHeaders(replayRequest, proc, body)
		replayRecorder := httptest.NewRecorder()
		replayRouter := chi.NewRouter()
		srv.Mount(replayRouter)
		replayRouter.ServeHTTP(replayRecorder, replayRequest)
		replay := decodeCloseIntentResponse(t, replayRecorder)
		if replayRecorder.Code != http.StatusOK || replay.Close == nil || !replay.Close.Allowed {
			t.Fatalf("shrunk processor metadata rejected exact replay: status=%d response=%+v", replayRecorder.Code, replay)
		}
	})
}

func TestFirstCloseAfterSchemaChangeIsCorrelatedWithoutReadingOldBody(t *testing.T) {
	t.Run("entity discard and save", func(t *testing.T) {
		srv, entity := setupManagedEventsServer(t, "", nil, nil)
		entity.Fields = append(entity.Fields, metadata.Field{Name: "Rich", Type: metadata.FieldTypeRichText})
		entity.Forms[0].Elements = append(entity.Forms[0].Elements, &metadata.FormElement{
			Kind: metadata.FormElementField, Name: "Rich", DataPath: "Объект.Rich",
		})
		discardBody := closeIntentBody(uuid.NewString(), "close", "client state")
		discardBody.Set("Rich", strings.Repeat("a", 2<<20))
		ensureEntityCloseIntentSchema(entity, discardBody)
		saveBody := closeIntentBody(uuid.NewString(), "ok", "client state")
		saveBody.Set("_close_mode", "save")
		saveBody.Set("Rich", strings.Repeat("b", 2<<20))
		ensureEntityCloseIntentSchema(entity, saveBody)

		entity.Fields = entity.Fields[:len(entity.Fields)-1]
		entity.Forms[0].Elements = entity.Forms[0].Elements[:len(entity.Forms[0].Elements)-1]
		execute := func(body url.Values) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodPost,
				"/ui/"+strings.ToLower(string(entity.Kind))+"/"+entity.Name+"/form-close-intent",
				strings.NewReader(body.Encode()))
			req.ContentLength = -1
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
			setEntityCloseIntentHeaders(req, body)
			recorder := httptest.NewRecorder()
			router := chi.NewRouter()
			srv.Mount(router)
			router.ServeHTTP(recorder, req)
			return recorder
		}

		discardRecorder := execute(discardBody)
		discard := decodeCloseIntentResponse(t, discardRecorder)
		if discardRecorder.Code != http.StatusOK || discard.Close == nil ||
			!discard.Close.Allowed || !discard.Close.Terminal {
			t.Fatalf("stale discard was not safely correlated: status=%d response=%+v", discardRecorder.Code, discard)
		}

		saveRecorder := execute(saveBody)
		save := decodeCloseIntentResponse(t, saveRecorder)
		if saveRecorder.Code != http.StatusConflict || save.Close == nil ||
			save.Close.Allowed || !save.Close.Reconcile || save.Close.Terminal {
			t.Fatalf("stale save did not remain open for reconciliation: status=%d response=%+v", saveRecorder.Code, save)
		}
	})

	t.Run("processor discard", func(t *testing.T) {
		form := processorExecutionForm(&metadata.FormElement{
			Kind: metadata.FormElementField, Name: "File", DataPath: "Объект.File", Type: "file",
		})
		proc := &processor.Processor{
			Name: "CloseFirstAfterShrink", Params: []processor.Param{{Name: "File", Type: "file"}},
			Forms: []*metadata.FormModule{form},
		}
		srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
		body := processorCloseIntentBody(proc, uuid.NewString())
		body.Set("File", strings.Repeat("a", 2<<20))
		ensureProcessorCloseIntentSchema(proc, body)
		forgedWrite := make(url.Values, len(body))
		for key, values := range body {
			forgedWrite[key] = append([]string(nil), values...)
		}
		forgedWrite.Set(processorServiceFieldName(proc.Params, "_close_intent_id"), uuid.NewString())
		forgedWrite.Set(processorServiceFieldName(proc.Params, "_close_mode"), "save")
		proc.Params = nil
		form.Elements = nil

		req := httptest.NewRequest(http.MethodPost, "/ui/processor/"+proc.Name+"/form-close-intent",
			strings.NewReader(body.Encode()))
		req.ContentLength = -1
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		setProcessorCloseIntentHeaders(req, proc, body)
		recorder := httptest.NewRecorder()
		router := chi.NewRouter()
		srv.Mount(router)
		router.ServeHTTP(recorder, req)
		response := decodeCloseIntentResponse(t, recorder)
		if recorder.Code != http.StatusOK || response.Close == nil ||
			!response.Close.Allowed || !response.Close.Terminal {
			t.Fatalf("stale processor close was not safely correlated: status=%d response=%+v", recorder.Code, response)
		}

		forgedReq := httptest.NewRequest(http.MethodPost, "/ui/processor/"+proc.Name+"/form-close-intent",
			strings.NewReader(forgedWrite.Encode()))
		forgedReq.ContentLength = -1
		forgedReq.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		setProcessorCloseIntentHeaders(forgedReq, proc, forgedWrite)
		forgedRecorder := httptest.NewRecorder()
		forgedRouter := chi.NewRouter()
		srv.Mount(forgedRouter)
		forgedRouter.ServeHTTP(forgedRecorder, forgedReq)
		forged := decodeCloseIntentResponse(t, forgedRecorder)
		if forgedRecorder.Code != http.StatusBadRequest || forged.Close == nil ||
			forged.Close.Allowed || forged.Close.Terminal {
			t.Fatalf("processor write-mode close was accepted: status=%d response=%+v", forgedRecorder.Code, forged)
		}
	})
}
