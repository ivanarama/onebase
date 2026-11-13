package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/project"
)

// YAML -> real GET/event/run/layout routes. Neither role variants nor admin
// selection may change the baseline form for an excluded operator.
func TestIncludeAdminObjectAndProcessorFormsThroughHTTP(t *testing.T) {
	testIncludeAdminFormsThroughHTTP(t, true)
}
func TestIncludeAdminExceptOnlyFormsThroughHTTP(t *testing.T) {
	testIncludeAdminFormsThroughHTTP(t, false)
}
func testIncludeAdminFormsThroughHTTP(t *testing.T, restrict bool) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("documents/probedoc.yaml", "name: ProbeDoc\nfields:\n  - {name: Note, type: string}\n")
	write("processors/probe.yaml", "name: Probe\nparams:\n  - {name: Note, type: string}\n")
	for _, owner := range []string{"ProbeDoc", "Probe"} {
		for _, variant := range []string{"Plain", "Compact"} {
			keys := ""
			if variant == "Compact" {
				keys = "  roles: [OTK, Chief]\n  except_roles: [Operator, Messenger]\n  include_admin: true\n"
				if !restrict {
					keys = "  except_roles: [Operator, Messenger]\n  include_admin: true\n"
				}
			}
			// Legacy processor forms use object/custom/blank kinds, not necessarily processor.
			kind := "object"
			if owner == "Probe" {
				kind = "custom"
			}
			form := "schema: onebase.form/v1\nform:\n  name: " + variant + "\n  kind: " + kind + "\n  entity: " + owner + "\n" + keys + "elements:\n  - kind: ПолеВвода\n    name: " + variant + "Field\n    data_path: Объект.Note\n    events: {ПриИзменении: Changed}\n  - kind: ТабличнаяЧасть\n    name: " + variant + "Grid\n    data_path: Форма.Rows\nattributes:\n  - name: Rows\n    type: ValueTable\n    columns:\n      - {name: Note, type: string}\n"
			write("forms/"+strings.ToLower(owner)+"/"+strings.ToLower(variant)+".form.yaml", form)
			write("forms/"+strings.ToLower(owner)+"/"+strings.ToLower(variant)+".form.os", "Процедура Changed()\nСообщить(\""+variant+"\");\nКонецПроцедуры\n")
		}
	}
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { proj.Close() })
	s, _ := newSubmitTestServer(t, proj.Entities)
	s.reg.LoadProcessors(proj.Processors)
	router := chi.NewRouter()
	s.Mount(router)
	for _, tc := range []struct {
		name    string
		roles   []string
		admin   bool
		variant string
	}{
		{"otk", []string{"OTK"}, false, "Compact"}, {"chief", []string{"Chief"}, false, "Compact"},
		{"admin", nil, true, "Compact"}, {"other", []string{"Other"}, false, "Plain"},
		{"operator", []string{"Operator"}, false, "Plain"}, {"messenger", []string{"Messenger"}, false, "Plain"},
		{"pair", []string{"Operator", "Messenger"}, false, "Plain"}, {"mixed", []string{"OTK", "Messenger"}, false, "Plain"},
		{"admin-operator", []string{"Operator"}, true, "Plain"},
	} {
		if !restrict && tc.name == "other" {
			tc.variant = "Compact"
		}
		t.Run(tc.name, func(t *testing.T) {
			u := &auth.User{Login: tc.name, IsAdmin: tc.admin}
			u.Roles = []*auth.Role{{Name: "Access", Permissions: auth.Permission{Documents: map[string][]string{"ProbeDoc": {"read", "write"}}, Processors: map[string][]string{"Probe": {"run"}}}}}
			for _, role := range tc.roles {
				u.Roles = append(u.Roles, &auth.Role{Name: role})
			}
			do := func(method, path string, body url.Values) *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequest(method, path, strings.NewReader(body.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req = req.WithContext(auth.ContextWithUser(req.Context(), u))
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				return rec
			}
			for _, path := range []string{"/ui/document/ProbeDoc/new", "/ui/processor/Probe"} {
				rec := do(http.MethodGet, path, nil)
				if rec.Code != 200 || !strings.Contains(rec.Body.String(), `data-ob-el="`+tc.variant+`Field"`) {
					t.Fatalf("GET %s: %d %.200s", path, rec.Code, rec.Body.String())
				}
			}
			for _, path := range []string{"/ui/document/ProbeDoc/form-event", "/ui/processor/Probe/form-event"} {
				rec := do(http.MethodPost, path, url.Values{"_element": {tc.variant + "Field"}, "_event": {"ПриИзменении"}, "Note": {"value"}})
				if rec.Code != 200 {
					t.Fatalf("event %s: %d %s", path, rec.Code, rec.Body.String())
				}
				resp := decodeFormEventResponse(t, rec.Body.Bytes())
				if len(resp.Messages) != 1 || resp.Messages[0] != tc.variant {
					t.Fatalf("event %s: %+v", path, resp)
				}
			}
			rec := do(http.MethodPost, "/ui/processor/Probe", url.Values{"Note": {"value"}})
			if rec.Code != 200 || !strings.Contains(rec.Body.String(), `data-ob-el="`+tc.variant+`Field"`) {
				t.Fatalf("run: %d %.300s", rec.Code, rec.Body.String())
			}
			rec = do(http.MethodPost, "/ui/_grid-order", url.Values{"grid": {"processor/Probe/" + tc.variant + "/" + tc.variant + "Grid"}, "order": {"[]"}})
			if rec.Code != http.StatusNoContent {
				t.Fatalf("layout: %d %s", rec.Code, rec.Body.String())
			}
			other := "Plain"
			if tc.variant == "Plain" {
				other = "Compact"
			}
			rec = do(http.MethodPost, "/ui/_grid-order", url.Values{"grid": {"processor/Probe/" + other + "/" + other + "Grid"}, "order": {"[]"}})
			if rec.Code != http.StatusNotFound {
				t.Fatalf("foreign layout accepted: %d", rec.Code)
			}
		})
	}
}
