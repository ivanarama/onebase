package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/llm"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
)

// Exercise the production HTTP routes and the actual chat tool dispatcher.
// Nil/absent rights must not turn into permission through another entry point.
func TestProcessorFailClosed_PublicPaths2407(t *testing.T) {
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "Fire",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Fire"},
	})
	form.ProgramAST = mustParse(t, `Процедура Fire()
  Сообщить("EVENT_MARKER");
КонецПроцедуры`)
	proc := &processor.Processor{Name: "Import", Forms: []*metadata.FormModule{form}}
	fixture, db := newProcessorFormEventExecutionServer(t, proc, mustParse(t, `Процедура Выполнить()
  Сообщить("RUN_MARKER");
КонецПроцедуры`))
	fixture.reg.LoadProcessors([]*processor.Processor{proc, {Name: "Other"}})
	s := New(fixture.reg, db, fixture.interp, nil, Config{}, nil)
	t.Cleanup(s.BeginShutdown)
	router := chi.NewRouter()
	s.Mount(router)
	role := func(policy string) *auth.Role {
		t.Helper()
		r, err := auth.ParseRole([]byte("name: Operator\npermissions:\n  ai_data_access: true\n" + policy))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, tc := range []struct {
		name  string
		user  *auth.User
		allow bool
	}{
		{"absent", &auth.User{Roles: []*auth.Role{role("")}}, false},
		{"null", &auth.User{Roles: []*auth.Role{role("  processors: null\n")}}, false},
		{"empty_map", &auth.User{Roles: []*auth.Role{role("  processors: {}\n")}}, false},
		{"listed", &auth.User{Roles: []*auth.Role{role("  processors:\n    Import: [run]\n")}}, true},
		{"unlisted", &auth.User{Roles: []*auth.Role{role("  processors:\n    Other: [run]\n")}}, false},
		{"compatibility", &auth.User{Roles: []*auth.Role{role("  processors_default: allow\n")}}, true},
		{"union_does_not_widen", &auth.User{Roles: []*auth.Role{role(""), role("  processors: {}\n")}}, false},
		{"union_explicit_grant", &auth.User{Roles: []*auth.Role{role(""), role("  processors:\n    Import: [run]\n")}}, true},
		{"admin", &auth.User{IsAdmin: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := func(method, path string, body url.Values) *httptest.ResponseRecorder {
				t.Helper()
				r := httptest.NewRequest(method, path, strings.NewReader(body.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				r = r.WithContext(auth.ContextWithUser(r.Context(), tc.user))
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				return w
			}
			menu := request(http.MethodGet, "/ui/", nil)
			if menu.Code != http.StatusOK {
				t.Fatalf("menu: status=%d", menu.Code)
			}
			if visible := strings.Contains(menu.Body.String(), "/ui/processor/import"); visible != tc.allow {
				t.Errorf("menu visible=%v, want %v", visible, tc.allow)
			}
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				response := request(method, "/ui/processor/Import", nil)
				want := http.StatusForbidden
				if tc.allow {
					want = http.StatusOK
				}
				if response.Code != want {
					t.Fatalf("%s: status=%d, want=%d, body=%s", method, response.Code, want, response.Body.String())
				}
				if method == http.MethodPost && tc.allow && !strings.Contains(response.Body.String(), "RUN_MARKER") {
					t.Fatal("permitted POST did not execute processor")
				}
			}
			if tc.name == "listed" || tc.name == "union_explicit_grant" {
				if response := request(http.MethodGet, "/ui/processor/Other", nil); response.Code != http.StatusForbidden {
					t.Errorf("explicit map opened unlisted processor: %d", response.Code)
				}
			}
			event := request(http.MethodPost, "/ui/processor/Import/form-event", processorClickBody("Fire"))
			if tc.allow {
				if event.Code != http.StatusOK || !strings.Contains(event.Body.String(), "EVENT_MARKER") {
					t.Fatalf("permitted form event did not execute: %d %s", event.Code, event.Body.String())
				}
			} else {
				response := decodeFormEventResponse(t, event.Body.Bytes())
				if response.OK || response.Error != "доступ запрещён" || strings.Contains(event.Body.String(), "EVENT_MARKER") {
					t.Errorf("forbidden form event executed: status=%d, body=%s", event.Code, event.Body.String())
				}
			}
			r := reqWithUser("/ui/ai/chat", tc.user)
			_, execute, pending := s.aiTools(r)
			if execute == nil || pending == nil {
				t.Fatal("chat tool dispatcher unavailable")
			}
			result := execute(r.Context(), llm.ToolCall{ID: "open", Name: "открыть_форму",
				Input: map[string]any{"вид": "processor", "сущность": "Import"}})
			if tc.allow {
				if result.IsError || len(pending.Actions) != 1 {
					t.Fatalf("permitted navigation: result=%+v actions=%+v", result, pending.Actions)
				}
			} else if !result.IsError || len(pending.Actions) != 0 {
				t.Fatalf("forbidden navigation granted: result=%+v actions=%+v", result, pending.Actions)
			}
		})
	}
}
