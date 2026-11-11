package ui

import (
	"context"
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

func resultTestServer(t *testing.T) (*Server, *metadata.Entity, *processor.Processor) {
	t.Helper()
	s, e, _ := setupDeleteActionServer(t)
	f := e.Forms[0]
	f.Elements = append(f.Elements, &metadata.FormElement{Kind: metadata.FormElementButton, Name: "Завершить", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Завершить"}})
	f.ProgramAST = mustParse(t, `
Процедура Завершить()
  Объект.Записать();
  ОткрытьФормуРезультата("Результат", Объект.Ссылка, "Документ");
КонецПроцедуры
`)
	mini := processorExecutionForm(
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Доп", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Доп"}},
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Номер", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Номер"}},
	)
	mini.ProgramAST = mustParse(t, `
Процедура Доп()
  ОткрытьФорму(Параметры.Документ);
  ЗакрытьФормуРезультата();
КонецПроцедуры
Процедура Номер()
  Сообщить(Параметры.Документ.Наименование);
КонецПроцедуры
`)
	proc := &processor.Processor{Name: "Результат", Params: []processor.Param{{Name: "Документ", Type: "reference:" + e.Name}}, Forms: []*metadata.FormModule{mini}}
	s.reg.LoadProcessors([]*processor.Processor{proc})
	return s, e, proc
}

func resultSourceEvent(t *testing.T, s *Server, e *metadata.Entity, values url.Values, users ...*auth.User) formEventResponse {
	t.Helper()
	req := httptest.NewRequest("POST", "/ui/catalog/"+e.Name+"/form-event", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rc := chi.NewRouteContext()
	rc.URLParams.Add("kind", "catalog")
	rc.URLParams.Add("entity", e.Name)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	if len(users) > 0 {
		req = req.WithContext(auth.ContextWithUser(req.Context(), users[0]))
	}
	rec := httptest.NewRecorder()
	s.handleManagedFormEvent(rec, req)
	return decodeFormEventResponse(t, rec.Body.Bytes())
}

func resultProcessorEvent(t *testing.T, s *Server, proc *processor.Processor, payload *resultFormPayload, element string, extra url.Values, users ...*auth.User) formEventResponse {
	t.Helper()
	if extra == nil {
		extra = url.Values{}
	}
	extra.Set("_element", element)
	extra.Set("_event", "Нажатие")
	req := httptest.NewRequest("POST", "/ui/processor/"+proc.Name+"/form-event?"+resultContextQuery+"="+payload.Token, strings.NewReader(extra.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rc := chi.NewRouteContext()
	rc.URLParams.Add("name", proc.Name)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	if len(users) > 0 {
		req = req.WithContext(auth.ContextWithUser(req.Context(), users[0]))
	}
	rec := httptest.NewRecorder()
	s.handleProcessorFormEvent(rec, req)
	return decodeFormEventResponse(t, rec.Body.Bytes())
}

func TestResultFormAuthorizationRecheckedOnCommands(t *testing.T) {
	s, e, proc := resultTestServer(t)
	permissions := auth.Permission{
		Catalogs:   map[string][]string{e.Name: {"read", "write"}},
		Processors: map[string][]string{proc.Name: {"run"}},
		RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
			e.Name: {"read": {Field: "Наименование", Op: "eq", Value: auth.RowValue{User: "login"}}},
		}},
	}
	owner := &auth.User{ID: "owner-id", Login: "owner", Roles: []*auth.Role{{Name: "ReaderWriter", Permissions: permissions}}}
	saved := resultSourceEvent(t, s, e, url.Values{"_event": {"Нажатие"}, "_element": {"Завершить"}, "Наименование": {"owner"}}, owner)
	if !saved.OK || saved.ResultForm == nil {
		t.Fatalf("authorized result failed: %+v", saved)
	}
	if got := resultProcessorEvent(t, s, proc, saved.ResultForm, "Номер", nil, owner); !got.OK {
		t.Fatalf("owner's independent command failed: %+v", got)
	}
	foreign := &auth.User{ID: "other-id", Login: "other", IsAdmin: true}
	if got := resultProcessorEvent(t, s, proc, saved.ResultForm, "Номер", nil, foreign); got.OK || len(got.Messages) != 0 {
		t.Fatalf("foreign context disclosed record: %+v", got)
	}
	// Same principal keeps the context identity, but now fails its row policy.
	changed := *owner
	changed.Login = "different-owner"
	if got := resultProcessorEvent(t, s, proc, saved.ResultForm, "Номер", nil, &changed); got.OK || len(got.Messages) != 0 {
		t.Fatalf("RLS changed but result still ran: %+v", got)
	}
	revoked := *owner
	revoked.Roles = []*auth.Role{{Permissions: auth.Permission{Processors: map[string][]string{proc.Name: {"run"}}}}}
	if got := resultProcessorEvent(t, s, proc, saved.ResultForm, "Номер", nil, &revoked); got.OK || len(got.Messages) != 0 {
		t.Fatalf("revoked read permission still disclosed result: %+v", got)
	}
}

func TestResultFormSavedIdentityAndOwnCommands(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[existing], func(t *testing.T) {
			s, e, proc := resultTestServer(t)
			values := url.Values{"_event": {"Нажатие"}, "_element": {"Завершить"}, "Наименование": {"RESULT-42"}}
			if existing {
				id := uuid.New()
				if err := s.store.Upsert(context.Background(), e.Name, id, map[string]any{"Наименование": "old"}, e); err != nil {
					t.Fatal(err)
				}
				values.Set("_id", id.String())
			}
			resp := resultSourceEvent(t, s, e, values)
			if !resp.OK || resp.ResultForm == nil || resp.Dirty == nil || *resp.Dirty {
				t.Fatalf("no committed result: %+v", resp)
			}
			mini := resultProcessorEvent(t, s, proc, resp.ResultForm, "Номер", url.Values{"Документ": {uuid.NewString()}})
			if !mini.OK || strings.Join(mini.Messages, "") != "RESULT-42" {
				t.Fatalf("result binding was lost/replaced: %+v", mini)
			}
			action := resultProcessorEvent(t, s, proc, resp.ResultForm, "Доп", nil)
			if !action.OK || !action.CloseResultForm || action.Navigation == nil {
				t.Fatalf("missing instance-local navigation + close: %+v", action)
			}
			// Each completion has an independent opaque identity even for one proc.
			second := resultSourceEvent(t, s, e, url.Values{"_event": {"Нажатие"}, "_element": {"Завершить"}, "Наименование": {"OTHER-99"}})
			if !second.OK || second.ResultForm.Token == resp.ResultForm.Token {
				t.Fatal("result instances share their identity")
			}
		})
	}
}

func TestResultFormFailureDoesNotPublish(t *testing.T) {
	for _, tail := range []string{`ВызватьИсключение "failed";`, `Объект.Наименование = "not saved";`} {
		s, e, _ := resultTestServer(t)
		e.Forms[0].ProgramAST = mustParse(t, `
Процедура Завершить()
 Объект.Записать();
 ОткрытьФормуРезультата("Результат", Объект.Ссылка, "Документ");
 `+tail+`
КонецПроцедуры`)
		resp := resultSourceEvent(t, s, e, url.Values{"_event": {"Нажатие"}, "_element": {"Завершить"}, "Наименование": {"saved"}})
		if resp.OK || resp.ResultForm != nil {
			t.Fatalf("failed/dirty completion published a result: %+v", resp)
		}
	}
}
