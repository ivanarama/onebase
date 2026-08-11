package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

func formEventReadGateFixture(t *testing.T) (*Server, *metadata.Entity, uuid.UUID) {
	t.Helper()
	button := &metadata.FormElement{
		Kind: metadata.FormElementButton,
		Name: "Check",
		Handlers: map[metadata.FormEventType]string{
			metadata.FormEventOnClick: "Run",
		},
	}
	form := managedObjectForm(button)
	form.ProgramAST = mustParse(t, `
Процедура Run()
	Сообщить("HANDLER_RAN");
	Объект.Probe = "handler-ran";
	Объект.Записать();
КонецПроцедуры
`)
	ent := &metadata.Entity{
		Name: "FormEventSecret",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Name", Type: metadata.FieldTypeString},
			{Name: "Owner", Type: metadata.FieldTypeString},
			{Name: "Probe", Type: metadata.FieldTypeString},
		},
		Forms: []*metadata.FormModule{form},
	}
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := srv.store.Upsert(t.Context(), ent.Name, id, map[string]any{
		"Name": "TOP_SECRET", "Owner": "alice", "Probe": "untouched",
	}, ent); err != nil {
		t.Fatal(err)
	}
	return srv, ent, id
}

func runFormEventAs(t *testing.T, srv *Server, ent *metadata.Entity, body url.Values, user *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	req := reqWithChi(http.MethodPost, "/ui/catalog/"+ent.Name+"/form-event", body,
		map[string]string{"entity": ent.Name})
	if user != nil {
		req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	}
	rec := httptest.NewRecorder()
	srv.handleManagedFormEvent(rec, req)
	return rec
}

func assertFormEventHandlerDidNotRun(t *testing.T, srv *Server, ent *metadata.Entity, id uuid.UUID, rec *httptest.ResponseRecorder) {
	t.Helper()
	body := rec.Body.String()
	if strings.Contains(body, "TOP_SECRET") || strings.Contains(body, "untouched") {
		t.Fatalf("denied event leaked stored state: %s", body)
	}
	if strings.Contains(body, "HANDLER_RAN") {
		t.Fatalf("denied event executed its handler: %s", body)
	}
	row, err := srv.store.GetByID(t.Context(), ent.Name, id, ent)
	if err != nil {
		t.Fatal(err)
	}
	if row["Probe"] != "untouched" {
		t.Fatalf("denied handler changed DB row: %#v", row)
	}
}

func TestManagedFormEvent_ExistingRecordRequiresReadPermissionBeforeHydration(t *testing.T) {
	srv, ent, id := formEventReadGateFixture(t)
	user := &auth.User{Login: "bob", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs: map[string][]string{ent.Name: {"write"}},
	}}}}
	body := url.Values{
		"_element": {"Check"}, "_event": {string(metadata.FormEventOnClick)},
		"_kind": {"object"}, "_id": {id.String()},
	}
	rec := runFormEventAs(t, srv, ent, body, user)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertFormEventHandlerDidNotRun(t, srv, ent, id, rec)
}

func TestManagedFormEvent_ForgedIDRequiresReadRowPolicyBeforeHydration(t *testing.T) {
	srv, ent, id := formEventReadGateFixture(t)
	user := &auth.User{Login: "bob", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs: map[string][]string{ent.Name: {"read", "write"}},
		RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
			ent.Name: {"read": {Field: "Owner", Op: "eq", Value: auth.RowValue{User: "login"}}},
		}},
	}}}}
	body := url.Values{
		"_element": {"Check"}, "_event": {string(metadata.FormEventOnClick)},
		"_kind": {"object"}, "_id": {id.String()},
	}
	rec := runFormEventAs(t, srv, ent, body, user)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertFormEventHandlerDidNotRun(t, srv, ent, id, rec)
}

func TestManagedFormEvent_RejectsInvalidOrMissingExistingIDBeforeHandler(t *testing.T) {
	srv, ent, storedID := formEventReadGateFixture(t)
	for _, tc := range []struct {
		name string
		id   string
		want int
	}{
		{name: "malformed", id: "not-a-uuid", want: http.StatusBadRequest},
		{name: "missing", id: uuid.NewString(), want: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := url.Values{
				"_element": {"Check"}, "_event": {string(metadata.FormEventOnClick)},
				"_kind": {"object"}, "_id": {tc.id},
			}
			rec := runFormEventAs(t, srv, ent, body, nil)
			if rec.Code != tc.want {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			assertFormEventHandlerDidNotRun(t, srv, ent, storedID, rec)
		})
	}
}
