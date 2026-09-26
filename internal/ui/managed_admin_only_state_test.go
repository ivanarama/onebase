package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestManagedAdminOnlyRemainsLockedAfterEvent(t *testing.T) {
	for _, groupCondition := range []bool{false, true} {
		for _, identity := range []string{"operator", "admin", "no-auth"} {
			t.Run(identity+map[bool]string{false: "/own", true: "/group"}[groupCondition], func(t *testing.T) {
				field := &metadata.FormElement{Kind: metadata.FormElementField, Name: "AdminName", DataPath: "Объект.Наименование", EditableAdminOnly: true, ReadOnlyWhen: "Ложь"}
				var root = field
				if groupCondition {
					field.ReadOnlyWhen = ""
					root = &metadata.FormElement{Kind: metadata.FormElementGroupBox, Name: "Group", ReadOnlyWhen: "Ложь", Children: []*metadata.FormElement{field}}
				}
				srv, ent := setupManagedEventsServer(t, "Процедура Refresh()\nКонецПроцедуры", nil, []*metadata.FormElement{root, {Kind: metadata.FormElementButton, Name: "Refresh", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Refresh"}}})
				var user *auth.User
				if identity != "no-auth" {
					srv.authRepo = auth.NewRepo(srv.store)
					user = &auth.User{Login: identity, IsAdmin: identity == "admin", Roles: []*auth.Role{{Permissions: auth.Permission{Catalogs: map[string][]string{ent.Name: {"read", "write"}}}}}}
				}
				req := reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/new", nil, map[string]string{"entity": ent.Name})
				if user != nil {
					req = req.WithContext(auth.ContextWithUser(req.Context(), user))
				}
				rec := httptest.NewRecorder()
				srv.form(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("form: %d %s", rec.Code, rec.Body.String())
				}
				dom := managedFormDOM(t, rec.Body.String())
				want := identity == "operator"
				if control := dom.control(t, "Наименование"); (control.ReadOnly || control.Disabled) != want {
					got := control.ReadOnly || control.Disabled
					t.Fatalf("initial readonly=%v want=%v", got, want)
				}
				body := url.Values{"_element": {"Refresh"}, "_event": {string(metadata.FormEventOnClick)}, "_kind": {"object"}, "Наименование": {"example"}}
				response := runFormEventAs(t, srv, ent, body, user)
				if response.Code != http.StatusOK {
					t.Fatalf("event: %d %s", response.Code, response.Body.String())
				}
				payload := decodeFormEventResponse(t, response.Body.Bytes())
				if !payload.OK {
					t.Fatalf("event failed: %s", payload.Error)
				}
				if payload.ElementStates == nil || payload.ElementStates.ReadOnly[field.Name] != want {
					t.Errorf("event state=%+v want readonly=%v", payload.ElementStates, want)
				}
				browser := применитьСостоянияВБраузере(t, dom, payload.ElementStates)
				if control := browser.control(t, "Наименование"); (control.ReadOnly || control.Disabled) != want {
					got := control.ReadOnly || control.Disabled
					t.Errorf("after event readonly=%v want=%v", got, want)
				}
			})
		}
	}
}
