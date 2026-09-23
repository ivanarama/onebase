package ui

import (
	"context"
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
	"github.com/ivantit66/onebase/internal/storage"
)

type panicAfterCommitChangePublisher struct{}

func (panicAfterCommitChangePublisher) PublishChange(context.Context, string, string, map[string]any, map[string]any) {
	panic("injected post-commit publisher panic")
}

func TestManagedFormCloseIntentPreservesIdentityAcrossPostCommitPublisherPanic(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, "", nil, nil)
	ent.NotifyChanges = true
	srv.entitySvc.ChangePublisher = panicAfterCommitChangePublisher{}
	body := closeIntentBody(uuid.NewString(), "ok", "committed once")
	body.Set("_close_mode", "save")

	first := executeFormCloseIntent(t, srv, ent, body)
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	response := decodeCloseIntentResponse(t, first)
	if response.Close == nil || response.Close.Allowed || !response.Close.Saved || response.SavedID == "" || response.Version != 1 {
		t.Fatalf("post-commit panic lost durable identity: %+v", response)
	}
	if _, err := uuid.Parse(response.SavedID); err != nil {
		t.Fatalf("savedId is not UUID: %q", response.SavedID)
	}

	second := executeFormCloseIntent(t, srv, ent, body)
	recovered := decodeCloseIntentResponse(t, second)
	if second.Code != http.StatusOK || recovered.Close == nil || recovered.Close.Allowed ||
		!recovered.Close.Saved || !recovered.Close.Reload || recovered.SavedID != response.SavedID || recovered.Version != 1 {
		t.Fatalf("post-commit replay did not return safe recovery: first=(%d %s) second=(%d %s)", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	rows, err := srv.store.List(context.Background(), ent.Name, ent, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("post-commit retry created %d rows, want 1", len(rows))
	}
}

func TestManagedFormCloseIntentPreservesHandlerWriteAcrossPostCommitPublisherPanic(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Объект.Записать();
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	ent.NotifyChanges = true
	srv.entitySvc.ChangePublisher = panicAfterCommitChangePublisher{}
	body := closeIntentBody(uuid.NewString(), "close", "handler committed once")

	first := executeFormCloseIntent(t, srv, ent, body)
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	response := decodeCloseIntentResponse(t, first)
	if response.Close == nil || response.Close.Allowed || !response.Close.Saved || response.SavedID == "" || response.Version != 1 {
		t.Fatalf("handler post-commit panic lost durable identity: %+v", response)
	}
	second := executeFormCloseIntent(t, srv, ent, body)
	recovered := decodeCloseIntentResponse(t, second)
	if second.Code != http.StatusOK || recovered.Close == nil || recovered.Close.Allowed ||
		!recovered.Close.Saved || !recovered.Close.Reload || recovered.SavedID != response.SavedID || recovered.Version != 1 {
		t.Fatalf("handler post-commit replay did not return safe recovery: first=(%d %s) second=(%d %s)", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	rows, err := srv.store.List(context.Background(), ent.Name, ent, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("handler post-commit retry created %d rows, want 1", len(rows))
	}
}

func TestManagedFormCloseIntentHandlerWriteKeepsLiveStateInsideDSLTransaction(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	НачатьТранзакцию();
	Объект.Записать();
	Объект.Наименование = "second in transaction";
	Объект.Записать();
	ЗафиксироватьТранзакцию();
	Отказ = Истина;
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	body := closeIntentBody(uuid.NewString(), "close", "first in transaction")

	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, body))
	if response.Close == nil || response.Close.Allowed || !response.Close.Saved || response.SavedID == "" || response.Version != 2 {
		t.Fatalf("DSL transaction lost live saved state: %+v", response)
	}
	rows, err := srv.store.List(context.Background(), ent.Name, ent, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["Наименование"] != "second in transaction" {
		t.Fatalf("two writes in one DSL transaction produced %#v, want one updated row", rows)
	}
}

func TestManagedFormCloseIntentSavePersistsBeforeBeforeCloseDeny(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Сообщить(Объект.Наименование);
	Отказ = Истина;
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	body := closeIntentBody(uuid.NewString(), "ok", "Сохранённый")
	body.Set("_close_mode", "save")

	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, body))
	if !response.OK || response.Close == nil || response.Close.Allowed || !response.Close.Saved {
		t.Fatalf("save + BeforeClose deny must persist and stay open: %+v", response)
	}
	if response.SavedID == "" || response.Version != 1 {
		t.Fatalf("canonical id/version missing: %+v", response)
	}
	if response.Dirty == nil || *response.Dirty {
		t.Fatalf("durable save + deny must leave the open form clean: %+v", response)
	}
	if len(response.Messages) != 1 || response.Messages[0] != "Сохранённый" {
		t.Fatalf("BeforeClose did not observe persisted object: %v", response.Messages)
	}
	id, err := uuid.Parse(response.SavedID)
	if err != nil {
		t.Fatalf("savedId is not UUID: %q", response.SavedID)
	}
	row, err := srv.store.GetByID(context.Background(), ent.Name, id, ent)
	if err != nil || row["Наименование"] != "Сохранённый" {
		t.Fatalf("record was not persisted before deny: row=%v err=%v", row, err)
	}
}

func TestManagedFormCloseIntentKeepsUnsavedBeforeCloseStateDirty(t *testing.T) {
	for _, tc := range []struct {
		name string
		tail string
	}{
		{name: "deny", tail: "Отказ = Истина;"},
		{name: "exception", tail: "ВызватьИсключение(\"boom\");"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Объект.Наименование = "Не записано обработчиком";
	`+tc.tail+`
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
			body := closeIntentBody(uuid.NewString(), "ok", "Записанный снимок")
			body.Set("_close_mode", "save")

			response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, body))
			if response.Close == nil || response.Close.Allowed || !response.Close.Saved || response.Dirty == nil || !*response.Dirty {
				t.Fatalf("unsaved BeforeClose mutation was not retained as dirty: %+v", response)
			}
			if got := response.Values["Наименование"]; got != "Не записано обработчиком" {
				t.Fatalf("unsaved handler value missing from response: %v", got)
			}
			id, err := uuid.Parse(response.SavedID)
			if err != nil {
				t.Fatalf("savedId is not UUID: %q", response.SavedID)
			}
			row, err := srv.store.GetByID(context.Background(), ent.Name, id, ent)
			if err != nil || row["Наименование"] != "Записанный снимок" {
				t.Fatalf("unsaved handler value leaked into storage: row=%v err=%v", row, err)
			}
		})
	}
}

func TestManagedFormCloseIntentKeepsMutationAfterHandlerWrite(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Объект.Наименование = "durable B";
	Объект.Записать();
	Объект.Наименование = "unsaved A";
	Отказ = Истина;
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	id := uuid.New()
	if err := srv.store.Upsert(context.Background(), ent.Name, id, map[string]any{"Наименование": "unsaved A"}, ent); err != nil {
		t.Fatal(err)
	}
	body := closeIntentBody(uuid.NewString(), "close", "unsaved A")
	body.Set("_id", id.String())
	body.Set("_version", "1")

	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, body))
	if response.Close == nil || response.Close.Allowed || !response.Close.Saved || response.Dirty == nil || !*response.Dirty {
		t.Fatalf("post-write mutation was not retained dirty: %+v", response)
	}
	if got, present := response.Values["Наименование"]; present && got != "unsaved A" {
		t.Fatalf("response replaced post-write mutation: %v", got)
	}
	row, err := srv.store.GetByID(context.Background(), ent.Name, id, ent)
	if err != nil || row["Наименование"] != "durable B" {
		t.Fatalf("durable write mismatch: row=%v err=%v", row, err)
	}
}

func TestManagedFormCloseIntentKeepsTableMutationAfterHandlerWrite(t *testing.T) {
	fixture := setupFormCtxServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Объект.Строки.Очистить();
	Строка = Объект.Строки.Добавить();
	Строка.Канал = "durable B";
	Объект.Записать();
	Объект.Строки.Очистить();
	Строка = Объект.Строки.Добавить();
	Строка.Канал = "unsaved A";
	Отказ = Истина;
КонецПроцедуры
`, nil)
	fixture.entity.Forms[0].Handlers = map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}
	body := closeIntentBody(uuid.NewString(), "ok", "rows")
	body.Set("_close_mode", "save")

	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, fixture.srv, fixture.entity, body))
	if response.Close == nil || response.Close.Allowed || !response.Close.Saved || response.Dirty == nil || !*response.Dirty {
		t.Fatalf("post-write table mutation was not retained dirty: %+v", response)
	}
	rows := response.TableParts["Строки"]
	if len(rows) != 1 || rows[0]["Канал"] != "unsaved A" {
		t.Fatalf("response replaced post-write table mutation: %v", rows)
	}
	id, err := uuid.Parse(response.SavedID)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := fixture.srv.store.GetTablePartRows(context.Background(), fixture.entity.Name, "Строки", id, fixture.entity.TableParts[0])
	if err != nil || len(durable) != 1 || durable[0]["Канал"] != "durable B" {
		t.Fatalf("durable table write mismatch: rows=%v err=%v", durable, err)
	}
}

func TestManagedFormCloseIntentHandlerWriteHonorsSubmittedVersion(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Объект.Записать();
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	id := uuid.New()
	ctx := context.Background()
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": "form A"}, ent); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": "concurrent B"}, ent); err != nil {
		t.Fatal(err)
	}
	body := closeIntentBody(uuid.NewString(), "close", "form A")
	body.Set("_id", id.String())
	body.Set("_version", "1")

	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, body))
	if response.Close == nil || response.Close.Allowed || response.Close.Saved || response.Error == "" {
		t.Fatalf("stale handler write did not fail closed: %+v", response)
	}
	row, err := srv.store.GetByID(ctx, ent.Name, id, ent)
	if err != nil || row["Наименование"] != "concurrent B" {
		t.Fatalf("stale handler write overwrote concurrent data: row=%v err=%v", row, err)
	}
}

func TestManagedFormCloseIntentDeniedDiscardDoesNotPromoteConcurrentVersion(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Отказ = Истина;
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	id := uuid.New()
	ctx := context.Background()
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": "form A"}, ent); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": "concurrent B"}, ent); err != nil {
		t.Fatal(err)
	}
	discard := closeIntentBody(uuid.NewString(), "close", "form A")
	discard.Set("_id", id.String())
	discard.Set("_version", "1")
	first := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, discard))
	if first.Close == nil || first.Close.Allowed || first.Close.Saved || first.Version != 0 {
		t.Fatalf("denied discard promoted concurrent version: %+v", first)
	}
	if _, leaked := first.Values["_version"]; leaked {
		t.Fatalf("transport values exposed an ungated version token: %+v", first.Values)
	}

	retry := closeIntentBody(uuid.NewString(), "ok", "form A")
	retry.Set("_close_mode", "save")
	retry.Set("_id", id.String())
	retry.Set("_version", "1")
	recorder := executeFormCloseIntent(t, srv, ent, retry)
	second := decodeCloseIntentResponse(t, recorder)
	if recorder.Code != http.StatusConflict || second.Close == nil || second.Close.Allowed || second.Close.Saved {
		t.Fatalf("stale retry bypassed optimistic conflict: status=%d response=%+v", recorder.Code, second)
	}
	row, err := srv.store.GetByID(ctx, ent.Name, id, ent)
	if err != nil || row["Наименование"] != "concurrent B" {
		t.Fatalf("stale retry overwrote concurrent data: row=%v err=%v", row, err)
	}
}

func TestManagedFormCloseIntentProtectsMaskedFieldsAcrossCloseWrites(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode string
	}{
		{name: "discard handler write", mode: "discard"},
		{name: "canonical save", mode: "save"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entity := &metadata.Entity{
				Name: "MaskedClose" + strings.ReplaceAll(tc.name, " ", ""), Kind: metadata.KindCatalog,
				Fields: []metadata.Field{
					{Name: "Наименование", Type: metadata.FieldTypeString},
					{Name: "Секрет", Type: metadata.FieldTypeString},
				},
			}
			program := `
Процедура ПроверитьЗакрытие(Отказ)
`
			if tc.mode == "discard" {
				program += "\tОбъект.Записать();\n"
			}
			program += `
	Отказ = Истина;
КонецПроцедуры
`
			entity.Forms = []*metadata.FormModule{{
				Name: "Object", Kind: "object", EntityName: entity.Name, LayoutKind: metadata.FormLayoutManaged,
				Elements: []*metadata.FormElement{
					{Kind: metadata.FormElementField, Name: "Name", DataPath: "Объект.Наименование"},
					{Kind: metadata.FormElementField, Name: "Secret", DataPath: "Объект.Секрет"},
				},
				Handlers:   map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"},
				ProgramAST: mustParse(t, program),
			}}
			srv, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
			id := uuid.New()
			if err := srv.store.Upsert(ctx, entity.Name, id, map[string]any{"Наименование": "before", "Секрет": "real-secret"}, entity); err != nil {
				t.Fatal(err)
			}
			user := &auth.User{ID: "masked", Login: "masked", Roles: []*auth.Role{{
				Permissions: auth.Permission{
					Catalogs: map[string][]string{entity.Name: {"read", "write"}},
					FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{
						entity.Name: {"Секрет": {Read: "mask_all"}},
					}},
				},
			}}}
			body := closeIntentBody(uuid.NewString(), "close", "before")
			body.Set("_id", id.String())
			body.Set("_version", "1")
			body.Set("_close_mode", tc.mode)
			body.Set("Секрет", "forged-secret")
			response := decodeCloseIntentResponse(t, executeFormCloseIntentAsUser(t, srv, entity, body, user))
			if response.Close == nil || response.Close.Allowed || !response.Close.Saved || response.Dirty == nil || *response.Dirty {
				t.Fatalf("masked close lifecycle is not durable/clean: %+v", response)
			}
			if got := response.Values["Секрет"]; got == "real-secret" || got == "forged-secret" {
				t.Fatalf("response disclosed or accepted protected value: %v", got)
			}
			row, err := srv.store.GetByID(ctx, entity.Name, id, entity)
			if err != nil || row["Секрет"] != "real-secret" {
				t.Fatalf("protected field was overwritten: row=%v err=%v", row, err)
			}
		})
	}
}

func executeFormCloseIntentAsUser(t *testing.T, srv *Server, entity *metadata.Entity, body url.Values, user *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	ensureEntityCloseIntentSchema(entity, body)
	kind := strings.ToLower(string(entity.Kind))
	req := httptest.NewRequest(http.MethodPost, "/ui/"+kind+"/"+entity.Name+"/form-close-intent", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	setEntityCloseIntentHeaders(req, body)
	req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	recorder := httptest.NewRecorder()
	router := chi.NewRouter()
	srv.Mount(router)
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestManagedFormCloseIntentDiscardMarksHandlerMutationDirty(t *testing.T) {
	for _, tc := range []struct {
		name string
		tail string
	}{
		{name: "deny", tail: "Отказ = Истина;"},
		{name: "exception", tail: "ВызватьИсключение(\"boom\");"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Объект.Наименование = "Изменено при закрытии";
	`+tc.tail+`
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
			id := uuid.New()
			if err := srv.store.Upsert(context.Background(), ent.Name, id, map[string]any{"Наименование": "Исходное"}, ent); err != nil {
				t.Fatal(err)
			}
			body := closeIntentBody(uuid.NewString(), "close", "Исходное")
			body.Set("_id", id.String())
			body.Set("_version", "1")
			response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, body))
			if response.Close == nil || response.Close.Allowed || response.Close.Saved || response.Dirty == nil || !*response.Dirty {
				t.Fatalf("discard response lost unsaved BeforeClose mutation: %+v", response)
			}
			if got := response.Values["Наименование"]; got != "Изменено при закрытии" {
				t.Fatalf("handler mutation missing from response: %v", got)
			}
			row, err := srv.store.GetByID(context.Background(), ent.Name, id, ent)
			if err != nil || row["Наименование"] != "Исходное" {
				t.Fatalf("discard mutation leaked into storage: row=%v err=%v", row, err)
			}
		})
	}
}

func TestManagedFormCloseIntentNewDiscardReportsOnlyHandlerMutationDirty(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mutation  string
		wantDirty bool
	}{
		{name: "no-op", wantDirty: false},
		{name: "mutation", mutation: `Объект.Наименование = "after";`, wantDirty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	`+tc.mutation+`
	Отказ = Истина;
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
			body := closeIntentBody(uuid.NewString(), "close", "before")
			response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, body))
			if response.Close == nil || response.Close.Allowed || response.Close.Saved || response.Dirty == nil || *response.Dirty != tc.wantDirty {
				t.Fatalf("new discard dirty=%v, want %v: %+v", response.Dirty, tc.wantDirty, response)
			}
		})
	}
}

func TestManagedFormCloseIntentKeepsReferenceMutationWithSameLabel(t *testing.T) {
	refs := &metadata.Entity{
		Name: "CloseRefItems", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: metadata.StandardCodeField, Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
		Numerator: &metadata.Numerator{Prefix: "R-", Length: 2, Period: "none"},
	}
	owner := &metadata.Entity{
		Name: "CloseRefOwner", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Связь", Type: metadata.FieldType("reference:" + refs.Name), RefEntity: refs.Name},
		},
	}
	owner.Forms = []*metadata.FormModule{{
		Name: "Object", Kind: "object", EntityName: owner.Name, LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "Name", DataPath: "Объект.Наименование"},
			{Kind: metadata.FormElementField, Name: "Ref", DataPath: "Объект.Связь"},
		},
		Handlers: map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"},
		ProgramAST: mustParse(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Объект.Связь = Справочники.CloseRefItems.НайтиПоКоду("R-B");
	Отказ = Истина;
КонецПроцедуры
`),
	}}
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{refs, owner})
	refA, refB := uuid.New(), uuid.New()
	for id, code := range map[uuid.UUID]string{refA: "R-A", refB: "R-B"} {
		if err := srv.store.Upsert(ctx, refs.Name, id, map[string]any{
			metadata.StandardCodeField: code, "Наименование": "Одинаковая метка",
		}, refs); err != nil {
			t.Fatal(err)
		}
	}
	body := closeIntentBody(uuid.NewString(), "ok", "owner")
	body.Set("_close_mode", "save")
	body.Set("Связь", refA.String())
	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, owner, body))
	if response.Close == nil || response.Close.Allowed || !response.Close.Saved || response.Dirty == nil || !*response.Dirty {
		t.Fatalf("same-label reference mutation was not retained as dirty: %+v", response)
	}
	if got := refValueString(response.Values["Связь"]); got != refB.String() {
		t.Fatalf("response reference = %q, want handler-assigned %s", got, refB)
	}
	id, err := uuid.Parse(response.SavedID)
	if err != nil {
		t.Fatal(err)
	}
	row, err := srv.store.GetByID(ctx, owner.Name, id, owner)
	if err != nil || refValueString(row["Связь"]) != refA.String() {
		t.Fatalf("unsaved reference mutation leaked/was not distinguished: row=%v err=%v", row, err)
	}
}

func TestManagedFormCloseIntentReturnsVersionWrittenByBeforeCloseAfterDeny(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Объект.Наименование = "ЗаписаноПередЗакрытием";
	Объект.Записать();
	Отказ = Истина;
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	body := closeIntentBody(uuid.NewString(), "ok", "ПервыйSave")
	body.Set("_close_mode", "save")

	first := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, body))
	if !first.OK || first.Close == nil || first.Close.Allowed || !first.Close.Saved {
		t.Fatalf("save + handler write + deny failed: %+v", first)
	}
	if first.SavedID == "" || first.Version != 2 {
		t.Fatalf("response lost final handler-written version: %+v", first)
	}
	id, err := uuid.Parse(first.SavedID)
	if err != nil {
		t.Fatalf("savedId is not UUID: %q", first.SavedID)
	}
	row, err := srv.store.GetByID(context.Background(), ent.Name, id, ent)
	if err != nil || row["Наименование"] != "ЗаписаноПередЗакрытием" || row["_version"] != int64(2) {
		t.Fatalf("BeforeClose write is not canonical: row=%v err=%v", row, err)
	}

	// The denied form remains open. Its next save must use the final version
	// returned above, not the stale version captured before BeforeClose.
	retry := closeIntentBody(uuid.NewString(), "ok", "СледующийSave")
	retry.Set("_close_mode", "save")
	retry.Set("_id", id.String())
	retry.Set("_version", "2")
	recorder := executeFormCloseIntent(t, srv, ent, retry)
	second := decodeCloseIntentResponse(t, recorder)
	if recorder.Code != http.StatusOK || !second.OK || second.Version != 4 || second.Close == nil || second.Close.Allowed {
		t.Fatalf("next save conflicted after denied close: status=%d response=%+v", recorder.Code, second)
	}
}

func TestManagedFormCloseIntentSaveWithoutBeforeCloseAndPopupResult(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, "", nil, nil)
	body := closeIntentBody(uuid.NewString(), "ok", "Новый контрагент")
	body.Set("_close_mode", "save_and_select")
	body.Set("_popup", "1")

	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, body))
	if !response.OK || response.Close == nil || !response.Close.Allowed || !response.Close.Saved {
		t.Fatalf("save without BeforeClose failed: %+v", response)
	}
	if response.SavedID == "" || response.SavedLabel != "Новый контрагент" || response.Version != 1 {
		t.Fatalf("popup did not receive canonical result: %+v", response)
	}
	if response.Close.FormURL == "" {
		t.Fatalf("saved form URL missing: %+v", response.Close)
	}
}

func TestManagedFormCloseIntentPopupReturnsLabelWrittenByBeforeClose(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие()
	Объект.Наименование = "Итоговая подпись";
	Объект.Записать();
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	body := closeIntentBody(uuid.NewString(), "ok", "Подпись первого save")
	body.Set("_close_mode", "save_and_select")
	body.Set("_popup", "1")

	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, body))
	if !response.OK || response.Close == nil || !response.Close.Allowed || !response.Close.Saved {
		t.Fatalf("popup save-and-select failed: %+v", response)
	}
	if response.SavedLabel != "Итоговая подпись" || response.Version != 2 {
		t.Fatalf("popup received stale pre-BeforeClose result: %+v", response)
	}
}

func TestManagedFormCloseIntentPopupCanRetryCreatedRecordAfterDeny(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Если Объект.Наименование = "Сначала отказ" Тогда
		Отказ = Истина;
	КонецЕсли;
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	firstBody := closeIntentBody(uuid.NewString(), "ok", "Сначала отказ")
	firstBody.Set("_close_mode", "save_and_select")
	firstBody.Set("_popup", "1")
	first := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, firstBody))
	if first.Close == nil || first.Close.Allowed || !first.Close.Saved || first.SavedID == "" || first.Version != 1 {
		t.Fatalf("initial popup deny did not return created identity: %+v", first)
	}

	retry := closeIntentBody(uuid.NewString(), "ok", "Повтор разрешён")
	retry.Set("_close_mode", "save_and_select")
	retry.Set("_popup", "1")
	retry.Set("_id", first.SavedID)
	retry.Set("_version", "1")
	second := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, retry))
	if !second.OK || second.Close == nil || !second.Close.Allowed || !second.Close.Saved || second.SavedID != first.SavedID || second.SavedLabel != "Повтор разрешён" {
		t.Fatalf("existing popup could not finish save-and-select retry: %+v", second)
	}
}

func TestManagedFormCloseIntentNewSaveRefreshesCommonModuleWrites(t *testing.T) {
	f := setupFormCtxServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Об = Объект.Ссылка.ПолучитьОбъект();
	Об.Наименование = "Изменено через базу";
	Об.Канал = "Изменён через базу";
	Строка = Об.Строки.Добавить();
	Строка.Канал = "Строка из базы";
	Об.Записать();
	Отказ = Истина;
КонецПроцедуры
`, nil)
	form := f.entity.Forms[0]
	form.Handlers = map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}
	body := closeIntentBody(uuid.NewString(), "ok", "Новая запись")
	body.Set("_close_mode", "save")

	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, f.srv, f.entity, body))
	if !response.OK || response.Close == nil || response.Close.Allowed || !response.Close.Saved {
		t.Fatalf("new close-save/common-module deny failed: %+v", response)
	}
	if response.Version == 0 || response.Dirty == nil || *response.Dirty {
		t.Fatalf("final common-module write was not adopted as durable state: %+v", response)
	}
	if got := response.Values["Канал"]; got != "Изменён через базу" {
		t.Fatalf("read-only/unplaced field stayed stale after common-module write: %v", got)
	}
	if got := response.Values["Наименование"]; got != "Изменено через базу" {
		t.Fatalf("placed editable field stayed stale after common-module write: %v", got)
	}
	rows := response.TableParts["Строки"]
	if len(rows) != 1 || rows[0]["Канал"] != "Строка из базы" {
		t.Fatalf("unplaced table part stayed stale after common-module write: %v", rows)
	}
}

func TestManagedFormCloseIntentPostUsesCanonicalPostingPipeline(t *testing.T) {
	doc := &metadata.Entity{
		Name: "ЗакрываемыйДокумент", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	doc.Forms = []*metadata.FormModule{{
		Name: "ФормаОбъекта", Kind: "object", EntityName: doc.Name, LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование",
		}},
	}}
	doc.Forms[0].ProgramAST = mustParse(t, `
Процедура ПроверитьЗакрытие()
	Если Объект.posted Тогда
		Сообщить("posted-visible");
	КонецЕсли;
КонецПроцедуры
`)
	doc.Forms[0].Handlers = map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{doc})
	body := closeIntentBody(uuid.NewString(), "post_and_close", "Проведён")
	body.Set("_close_mode", "post")

	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, doc, body))
	if !response.OK || response.Close == nil || !response.Close.Allowed || !response.Close.Saved {
		t.Fatalf("post-and-close failed: %+v", response)
	}
	if len(response.Messages) != 1 || response.Messages[0] != "posted-visible" {
		t.Fatalf("BeforeClose did not observe canonical posted=true: %+v", response)
	}
	id, err := uuid.Parse(response.SavedID)
	if err != nil {
		t.Fatalf("savedId is not UUID: %q", response.SavedID)
	}
	row, err := srv.store.GetByID(context.Background(), doc.Name, id, doc)
	if err != nil || !asBool(row["posted"]) {
		t.Fatalf("close mode post bypassed canonical posting: row=%v err=%v", row, err)
	}
}

func TestManagedFormCloseIntentDiscardRestoresPersistedServiceFields(t *testing.T) {
	doc := &metadata.Entity{
		Name: "CloseServiceDocument", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	doc.Forms = []*metadata.FormModule{{
		Name: "Object", Kind: "object", EntityName: doc.Name, LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "Number", DataPath: "Объект.Номер"}},
		Handlers: map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"},
		ProgramAST: mustParse(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Если Объект.posted Тогда Сообщить("posted-visible"); КонецЕсли;
	Если Объект.deletion_mark Тогда Сообщить("deletion-visible"); КонецЕсли;
	Отказ = Истина;
КонецПроцедуры
`),
	}}
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{doc})
	id := uuid.New()
	if err := srv.store.Upsert(ctx, doc.Name, id, map[string]any{"Номер": "D-1", "posted": true}, doc); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetPosted(ctx, doc.Name, id, true); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.MarkForDeletion(ctx, doc.Name, id, true); err != nil {
		t.Fatal(err)
	}
	body := closeIntentBody(uuid.NewString(), "close", "")
	body.Del("Наименование")
	body.Set("Номер", "D-1")
	body.Set("_id", id.String())
	body.Set("_version", "2")
	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, doc, body))
	if response.Close == nil || response.Close.Allowed || response.Dirty == nil || *response.Dirty {
		t.Fatalf("discard service-state response is not clean/denied: %+v", response)
	}
	got := strings.Join(response.Messages, "|")
	if !strings.Contains(got, "posted-visible") || !strings.Contains(got, "deletion-visible") {
		t.Fatalf("BeforeClose did not see persisted service fields: %v", response.Messages)
	}
}

func TestManagedFormCloseIntentBeforeCloseSeesAssignedIdentityNumberAndVersion(t *testing.T) {
	doc := &metadata.Entity{
		Name: "CloseCanonicalDocument", Kind: metadata.KindDocument,
		Fields:    []metadata.Field{{Name: metadata.StandardNumberField, Type: metadata.FieldTypeString}},
		Numerator: &metadata.Numerator{Prefix: "C-", Length: 4, Period: "none"},
	}
	doc.Forms = []*metadata.FormModule{{
		Name: "Object", Kind: "object", EntityName: doc.Name, LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "Number", DataPath: "Объект." + metadata.StandardNumberField}},
		Handlers: map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"},
		ProgramAST: mustParse(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Если ЗначениеЗаполнено(Объект.Ссылка) И ЗначениеЗаполнено(Объект.Номер) И Объект._version = 1 Тогда
		Сообщить("canonical-visible");
	КонецЕсли;
	Отказ = Истина;
КонецПроцедуры
`),
	}}
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{doc})
	body := closeIntentBody(uuid.NewString(), "ok", "")
	body.Del("Наименование")
	body.Set("_close_mode", "save")
	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, doc, body))
	if response.Close == nil || response.Close.Allowed || !response.Close.Saved || response.Version != 1 {
		t.Fatalf("canonical save-before-close failed: %+v", response)
	}
	if !strings.Contains(strings.Join(response.Messages, "|"), "canonical-visible") {
		t.Fatalf("BeforeClose did not observe assigned id/number/version: %+v", response)
	}
}

func TestManagedFormCloseIntentCopyPreservesUnplacedTableParts(t *testing.T) {
	entity := &metadata.Entity{
		Name: "КопируемаяКарточка", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{{
			Name:   "СкрытыеСтроки",
			Fields: []metadata.Field{{Name: "Текст", Type: metadata.FieldTypeString}},
		}},
	}
	entity.Forms = []*metadata.FormModule{{
		Name: "ФормаОбъекта", Kind: "object", EntityName: entity.Name, LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование",
		}},
	}}
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
	sourceID := uuid.New()
	if err := srv.store.Upsert(ctx, entity.Name, sourceID, map[string]any{"Наименование": "Источник"}, entity); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpsertTablePartRows(ctx, entity.Name, "СкрытыеСтроки", sourceID,
		[]map[string]any{{"Текст": "Каноническая строка"}}, entity.TableParts[0]); err != nil {
		t.Fatal(err)
	}
	body := closeIntentBody(uuid.NewString(), "ok", "Копия")
	body.Set("_close_mode", "save")
	body.Set(copySourceFormField, sourceID.String())

	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, entity, body))
	if !response.OK || response.Close == nil || !response.Close.Allowed || !response.Close.Saved {
		t.Fatalf("copy close-save failed: %+v", response)
	}
	copyID, err := uuid.Parse(response.SavedID)
	if err != nil {
		t.Fatalf("savedId is not UUID: %q", response.SavedID)
	}
	rows, err := srv.store.GetTablePartRows(ctx, entity.Name, "СкрытыеСтроки", copyID, entity.TableParts[0])
	if err != nil || len(rows) != 1 || rows[0]["Текст"] != "Каноническая строка" {
		t.Fatalf("close-save lost canonical copied table part: rows=%v err=%v", rows, err)
	}
}

func TestManagedFormCloseIntentFailedSaveNeverCallsBeforeClose(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие()
	Сообщить("НЕ ДОЛЖЕН ВЫЗЫВАТЬСЯ");
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	id := uuid.New()
	if err := srv.store.Upsert(context.Background(), ent.Name, id, map[string]any{"Наименование": "Первая"}, ent); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.Upsert(context.Background(), ent.Name, id, map[string]any{"Наименование": "Вторая"}, ent); err != nil {
		t.Fatal(err)
	}
	body := closeIntentBody(uuid.NewString(), "ok", "Мой ввод")
	body.Set("_close_mode", "save")
	body.Set("_id", id.String())
	body.Set("_version", "1")

	recorder := executeFormCloseIntent(t, srv, ent, body)
	response := decodeCloseIntentResponse(t, recorder)
	if recorder.Code != http.StatusConflict || response.OK || response.Close == nil || response.Close.Allowed || response.Close.Saved {
		t.Fatalf("stale save did not fail closed: status=%d response=%+v", recorder.Code, response)
	}
	if len(response.Messages) != 0 {
		t.Fatalf("BeforeClose ran after failed save: %v", response.Messages)
	}
	row, err := srv.store.GetByID(context.Background(), ent.Name, id, ent)
	if err != nil || row["Наименование"] != "Вторая" {
		t.Fatalf("failed save changed persisted row: row=%v err=%v", row, err)
	}
}

func TestManagedFormCloseIntentDiscardAdoptsExistingHandlerWrite(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Объект.Наименование = "saved by close";
	Объект.Записать();
	Отказ = Истина;
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	id := uuid.New()
	if err := srv.store.Upsert(context.Background(), ent.Name, id, map[string]any{"Наименование": "before"}, ent); err != nil {
		t.Fatal(err)
	}
	body := closeIntentBody(uuid.NewString(), "close", "before")
	body.Set("_id", id.String())
	body.Set("_version", "1")
	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, body))
	if response.Close == nil || response.Close.Allowed || !response.Close.Saved || response.SavedID != id.String() || response.Version != 2 {
		t.Fatalf("discard handler write did not publish durable identity/version: %+v", response)
	}
}

func TestManagedFormCloseIntentAbsentScalarAttributeStaysClean(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Отказ = Истина;
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	ent.Forms[0].Attributes = append(ent.Forms[0].Attributes, &metadata.FormAttribute{Name: "Неприсланный", TypeRef: "string"})
	body := closeIntentBody(uuid.NewString(), "ok", "saved")
	body.Set("_close_mode", "save")
	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, body))
	if response.Dirty == nil || *response.Dirty {
		t.Fatalf("absent untouched scalar attribute made saved form dirty: %+v", response)
	}
}

func TestManagedFormCloseIntentRejectsSaveForNonObjectForm(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, "", nil, nil)
	ent.Forms = append(ent.Forms, &metadata.FormModule{Name: "List", Kind: "list", EntityName: ent.Name, LayoutKind: metadata.FormLayoutManaged})
	body := closeIntentBody(uuid.NewString(), "ok", "must not save")
	body.Set("_kind", "list")
	body.Set("_close_mode", "save")
	recorder := executeFormCloseIntent(t, srv, ent, body)
	response := decodeCloseIntentResponse(t, recorder)
	if recorder.Code != http.StatusBadRequest || response.Close == nil || response.Close.Allowed || response.Close.Saved {
		t.Fatalf("non-object form accepted save close mode: status=%d response=%+v", recorder.Code, response)
	}
}

func TestManagedFormCloseIntentNewHierarchyPreservesServiceFields(t *testing.T) {
	entity := &metadata.Entity{
		Name: "CloseHierarchy", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	entity.Forms = []*metadata.FormModule{{
		Name: "Object", Kind: "object", EntityName: entity.Name, LayoutKind: metadata.FormLayoutManaged,
		Attributes: []*metadata.FormAttribute{{Name: "parent_id", TypeRef: "string"}},
		Elements:   []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "Name", DataPath: "Объект.Наименование"}},
	}}
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
	parent := uuid.New()
	if err := srv.store.Upsert(ctx, entity.Name, parent, map[string]any{"Наименование": "parent", "is_folder": true}, entity); err != nil {
		t.Fatal(err)
	}
	body := closeIntentBody(uuid.NewString(), "ok", "child group")
	body.Set("_close_mode", "save")
	body.Set("parent_id", parent.String())
	body.Set("is_folder", "true")
	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, entity, body))
	id, err := uuid.Parse(response.SavedID)
	if err != nil {
		t.Fatalf("close save did not return id: %+v", response)
	}
	row, err := srv.store.GetByID(ctx, entity.Name, id, entity)
	if err != nil || !asBool(row["is_folder"]) || refValueString(row["parent_id"]) != parent.String() {
		t.Fatalf("close save lost hierarchy service fields: row=%v err=%v", row, err)
	}
}

func TestManagedFormCloseIntentExistingHierarchyPreservesSubmittedMove(t *testing.T) {
	entity := &metadata.Entity{
		Name: "CloseHierarchyMove", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	entity.Forms = []*metadata.FormModule{{
		Name: "Object", Kind: "object", EntityName: entity.Name, LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "Name", DataPath: "Объект.Наименование"}},
	}}
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
	parentA, parentB, child := uuid.New(), uuid.New(), uuid.New()
	for id, name := range map[uuid.UUID]string{parentA: "parent A", parentB: "parent B"} {
		if err := srv.store.Upsert(ctx, entity.Name, id, map[string]any{"Наименование": name, "is_folder": true}, entity); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.store.Upsert(ctx, entity.Name, child, map[string]any{
		"Наименование": "child", "parent_id": parentA.String(), "is_folder": false,
	}, entity); err != nil {
		t.Fatal(err)
	}
	body := closeIntentBody(uuid.NewString(), "ok", "child moved")
	body.Set("_close_mode", "save")
	body.Set("_id", child.String())
	body.Set("_version", "1")
	body.Set("parent_id", parentB.String())
	body.Set("is_folder", "true")
	response := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, entity, body))
	if response.Close == nil || !response.Close.Allowed || !response.Close.Saved {
		t.Fatalf("existing hierarchy close-save failed: %+v", response)
	}
	row, err := srv.store.GetByID(ctx, entity.Name, child, entity)
	if err != nil || refValueString(row["parent_id"]) != parentB.String() || !asBool(row["is_folder"]) {
		t.Fatalf("submitted hierarchy move was replaced by persisted state: row=%v err=%v", row, err)
	}
}

func TestProcessorFormCloseIntentRejectsSaveModes(t *testing.T) {
	form := processorExecutionForm()
	proc := &processor.Processor{Name: "NoFakeProcessorSave", Forms: []*metadata.FormModule{form}}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	body := processorCloseIntentBody(proc, uuid.NewString())
	body.Set(processorServiceFieldName(proc.Params, "_close_mode"), "save")
	recorder := executeProcessorCloseIntent(t, srv, proc, body)
	response := decodeCloseIntentResponse(t, recorder)
	if recorder.Code != http.StatusBadRequest || response.Close == nil || response.Close.Allowed {
		t.Fatalf("processor accepted fake save mode: status=%d response=%+v", recorder.Code, response)
	}
}

func TestProcessorFormCloseIntentReportsOnlyHandlerMutationsDirty(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mutation  string
		wantDirty bool
	}{
		{name: "no-op", wantDirty: false},
		{name: "mutation", mutation: `Объект.Имя = "after";`, wantDirty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			program := mustParse(t, `
Процедура ПроверитьЗакрытие(Отказ)
	`+tc.mutation+`
	Отказ = Истина;
КонецПроцедуры
`)
			form := processorExecutionForm(&metadata.FormElement{
				Kind: metadata.FormElementField, Name: "Name", DataPath: "Объект.Имя",
			})
			form.Handlers = map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}
			form.ProgramAST = program
			proc := &processor.Processor{
				Name:   "ProcessorCloseDirty" + tc.name,
				Params: []processor.Param{{Name: "Имя", Type: "string"}},
				Forms:  []*metadata.FormModule{form},
			}
			srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
			body := processorCloseIntentBody(proc, uuid.NewString())
			body.Set("Имя", "before")
			response := decodeCloseIntentResponse(t, executeProcessorCloseIntent(t, srv, proc, body))
			if response.Close == nil || response.Close.Allowed || response.Dirty == nil || *response.Dirty != tc.wantDirty {
				t.Fatalf("processor transient dirty=%v, want %v: %+v", response.Dirty, tc.wantDirty, response)
			}
		})
	}
}
