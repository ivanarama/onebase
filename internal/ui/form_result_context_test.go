package ui

import (
	"context"
	"github.com/go-chi/chi/v5"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
)

func TestResultContextGuardThroughHTTP(t *testing.T) {
	for _, phase := range []metadata.FormEventType{metadata.FormEventOnClick, metadata.FormEventOnAnswer} {
		t.Run(string(phase), func(t *testing.T) {
			for _, name := range []string{"valid", "nonadmin-valid", "read-revoked", "rls-denied", "missing", "ordinary-event", "foreign", "expired", "wrong-processor", "wrong-reference-kind", "wrong-reference-id", "wrong-posted-reference", "nonce-not-bearer", "missing-actor", "object-form"} {
				t.Run(name, func(t *testing.T) {
					s, entity, proc := resultTestServer(t)
					owner := &auth.User{ID: uuid.NewString(), Login: "guard-owner", IsAdmin: true}
					if name == "nonadmin-valid" || name == "read-revoked" || name == "rls-denied" {
						owner.IsAdmin = false
						owner.Roles = []*auth.Role{{Name: "GuardRights", Permissions: auth.Permission{Catalogs: map[string][]string{entity.Name: {"read", "write"}}, Processors: map[string][]string{proc.Name: {"run"}}}}}
					}
					saved := resultSourceEvent(t, s, entity, url.Values{"_event": {"Нажатие"}, "_element": {"Завершить"}, "Наименование": {"Synthetic source"}}, owner)
					if !saved.OK || saved.ResultForm == nil {
						t.Fatalf("cannot prepare own result: %+v", saved)
					}
					payload := *saved.ResultForm
					original := s.resultForms[payload.Token]
					id := uuid.MustParse(original.request.reference.UUID)
					before, err := s.store.GetByID(context.Background(), entity.Name, id, entity)
					if err != nil {
						t.Fatal(err)
					}
					form := proc.Forms[0]
					form.Elements = append(form.Elements,
						&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Guard", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Guard", metadata.FormEventOnAnswer: "Guard"}},
						&metadata.FormElement{Kind: metadata.FormElementField, Name: "GuardChange", DataPath: "Объект.Choice", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnChange: "Guard"}},
					)
					form.ProgramAST = mustParse(t, `
Процедура Guard()
 Контекст = КонтекстФормыРезультата();
 Второй = ResultFormContext();
 Если Контекст.ИДКонтекста <> Второй.ИДКонтекста Тогда ВызватьИсключение "unstable nonce"; КонецЕсли;
 Сообщить(Контекст.Пользователь.Имя);
 Сообщить(Контекст.Пользователь.ИД);
 Сообщить(Контекст.Обработка);
 Сообщить(Строка(Контекст.Ссылка.УникальныйИдентификатор()));
 Сообщить(ТипЗнч(Контекст.Ссылка));
 Сообщить(Контекст.ИДКонтекста);
КонецПроцедуры
`)
					actor, target := owner, proc
					extra := url.Values{}
					switch name {
					case "read-revoked":
						copyUser := *owner
						copyUser.Roles = []*auth.Role{{Name: "Revoked", Permissions: auth.Permission{Processors: map[string][]string{proc.Name: {"run"}}}}}
						actor = &copyUser
					case "rls-denied":
						copyUser := *owner
						copyUser.Roles = []*auth.Role{{Name: "Restricted", Permissions: auth.Permission{Catalogs: map[string][]string{entity.Name: {"read", "write"}}, Processors: map[string][]string{proc.Name: {"run"}}, RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{entity.Name: {"read": {Field: "Наименование", Op: "eq", Value: auth.RowValue{Literal: "different row"}}}}}}}}
						actor = &copyUser
					case "missing":
						payload.Token = ""
					case "ordinary-event":
						extra.Set("_event", string(metadata.FormEventOnChange))
					case "foreign":
						actor = &auth.User{ID: uuid.NewString(), Login: "other", IsAdmin: true}
					case "expired":
						c := original
						c.expires = time.Now().Add(-time.Second)
						s.resultForms[payload.Token] = c
					case "wrong-processor":
						other := *proc
						other.Name = "OtherResult"
						target = &other
						s.reg.LoadProcessors([]*processor.Processor{proc, target})
					case "wrong-reference-kind":
						c := original
						c.request.reference.Kind = metadata.KindDocument
						s.resultForms[payload.Token] = c
					case "wrong-reference-id":
						c := original
						c.request.reference.UUID = uuid.NewString()
						s.resultForms[payload.Token] = c
					case "wrong-posted-reference":
						extra.Set(original.request.parameter, uuid.NewString())
					case "nonce-not-bearer":
						payload.Token = resultContextNonce(payload.Token)
					case "missing-actor":
						actor = nil
					}
					cacheBefore := make(map[string]resultFormContext)
					for key, value := range s.resultForms {
						cacheBefore[key] = value
					}
					var response formEventResponse
					if name == "object-form" {
						entity.Forms[0].Elements = append(entity.Forms[0].Elements, &metadata.FormElement{Kind: metadata.FormElementButton, Name: "Guard", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Guard"}})
						entity.Forms[0].ProgramAST = mustParse(t, "Процедура Guard()\n КонтекстФормыРезультата();\nКонецПроцедуры")
						response = resultSourceEvent(t, s, entity, url.Values{"_event": {"Нажатие"}, "_element": {"Guard"}, "_id": {id.String()}}, owner)
					} else if name == "ordinary-event" {
						// The ordinary event has a valid bearer, but still cannot acquire
						// authorization intended only for a declared command.
						request := url.Values{"_element": {"GuardChange"}, "_event": {string(metadata.FormEventOnChange)}}
						response = resultContextEvent(t, s, target, &payload, request, actor)
					} else {
						extra.Set("_element", "Guard")
						extra.Set("_event", string(phase))
						extra.Set("_question_answer", "Да")
						response = resultContextEvent(t, s, target, &payload, extra, actor)
					}
					if response.CloseResultForm || response.Navigation != nil || response.ResultForm != nil {
						t.Fatalf("guard produced a UI effect: %+v", response)
					}
					if commands := reflect.ValueOf(response).FieldByName("UICommands"); commands.IsValid() && commands.Len() != 0 {
						t.Fatal("guard emitted UI commands")
					}
					if name == "valid" || name == "nonadmin-valid" {
						if !response.OK || len(response.Messages) != 6 {
							t.Fatalf("valid context: %+v", response)
						}
						want := []string{owner.Login, owner.ID, proc.Name, id.String(), metadata.RefTypeName(entity.Kind, entity.Name), resultContextNonce(payload.Token)}
						if !reflect.DeepEqual(response.Messages, want) {
							t.Fatalf("context values: got%q want%q", response.Messages, want)
						}
						if strings.Contains(strings.Join(response.Messages, "|"), payload.Token) {
							t.Fatal("bearer leaked")
						}
					} else if response.OK || len(response.Messages) != 0 {
						t.Fatalf("unsafe guard success: %+v", response)
					}
					if name == "ordinary-event" || name == "missing" || name == "object-form" {
						if !strings.Contains(response.Error, resultContextRefusal) {
							t.Fatalf("did not reach the intended guard: %s", response.Error)
						}
					}
					after, err := s.store.GetByID(context.Background(), entity.Name, id, entity)
					if err != nil || !reflect.DeepEqual(before, after) {
						t.Fatalf("source row changed: %v", err)
					}
					if !reflect.DeepEqual(cacheBefore, s.resultForms) {
						t.Fatal("readonly guard changed context cache")
					}
				})
			}
		})
	}
}

// Unlike the legacy convenience helper, preserve the actual event type.
func resultContextEvent(t *testing.T, s *Server, p *processor.Processor, payload *resultFormPayload, values url.Values, user *auth.User) formEventResponse {
	t.Helper()
	r := httptest.NewRequest("POST", "/ui/processor/"+url.PathEscape(p.Name)+"/form-event?"+resultContextQuery+"="+url.QueryEscape(payload.Token), strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	route := chi.NewRouteContext()
	route.URLParams.Add("name", p.Name)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, route))
	if user != nil {
		r = r.WithContext(auth.ContextWithUser(r.Context(), user))
	}
	rec := httptest.NewRecorder()
	s.handleProcessorFormEvent(rec, r)
	return decodeFormEventResponse(t, rec.Body.Bytes())
}

// The normal multiline dialog remains on the original button. Both phases
// independently validate the current result context; answering is not a grant.
func TestResultContextQuestionAnswerThroughHTTP(t *testing.T) {
	for _, mode := range []string{"valid", "expired-after-question", "foreign-after-question", "revoked-after-question", "answer-only-button", "form-answer", "metadata-command"} {
		t.Run(mode, func(t *testing.T) {
			s, entity, proc := resultTestServer(t)
			owner := &auth.User{ID: uuid.NewString(), Login: "answer-owner", Roles: []*auth.Role{{Name: "Own", Permissions: auth.Permission{Catalogs: map[string][]string{entity.Name: {"read", "write"}}, Processors: map[string][]string{proc.Name: {"run"}}}}}}
			saved := resultSourceEvent(t, s, entity, url.Values{"_event": {"Нажатие"}, "_element": {"Завершить"}, "Наименование": {"Synthetic answer"}}, owner)
			if !saved.OK || saved.ResultForm == nil {
				t.Fatalf("prepare: %+v", saved)
			}
			form := proc.Forms[0]
			button := &metadata.FormElement{Kind: metadata.FormElementButton, Name: "Ask", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Ask", metadata.FormEventOnAnswer: "Answer"}}
			form.Elements = append(form.Elements, button)
			form.ProgramAST = mustParse(t, `
Процедура Ask()
 К=КонтекстФормыРезультата();
 Если ЗначениеЗаполнено(ВопросОтвет) Тогда
  Сообщить(К.ИДКонтекста);
  Сообщить(ДиалогПоля.Примечание);
  Возврат;
 КонецЕсли;
 Поля=Новый Массив;
 Поля.Добавить(Новый Структура("имя, правка, многострочное", "Примечание", Истина, Истина));
 ПоказатьВопрос("Изменить?", "ДаНет", "", Поля);
КонецПроцедуры
Процедура Answer()
 К=ResultFormContext();
 Сообщить(К.ИДКонтекста);
 Сообщить(ДиалогПоля.Примечание);
КонецПроцедуры`)
			target := "Ask"
			if mode == "metadata-command" {
				target = "AskCommand"
				form.Elements = form.Elements[:len(form.Elements)-1]
				form.Commands = append(form.Commands, &metadata.FormCommand{Name: target, Action: "Ask"})
			}
			first := resultContextEvent(t, s, proc, saved.ResultForm, url.Values{"_element": {target}, "_event": {"Нажатие"}}, owner)
			if !first.OK || first.Question == nil || len(first.Question.Fields) != 1 || !first.Question.Fields[0].Multiline {
				t.Fatalf("question: %+v", first)
			}
			actor := owner
			switch mode {
			case "expired-after-question":
				c := s.resultForms[saved.ResultForm.Token]
				c.expires = time.Now().Add(-time.Second)
				s.resultForms[saved.ResultForm.Token] = c
			case "foreign-after-question":
				clone := *owner
				clone.ID = uuid.NewString()
				actor = &clone
			case "revoked-after-question":
				clone := *owner
				clone.Roles = []*auth.Role{{Name: "Revoked", Permissions: auth.Permission{Processors: map[string][]string{proc.Name: {"run"}}}}}
				actor = &clone
			case "answer-only-button":
				delete(button.Handlers, metadata.FormEventOnClick)
			case "form-answer":
				form.Handlers = map[metadata.FormEventType]string{metadata.FormEventOnAnswer: "Answer"}
				target = ""
			}
			original := s.resultForms[saved.ResultForm.Token]
			id := uuid.MustParse(original.request.reference.UUID)
			before, err := s.store.GetByID(context.Background(), entity.Name, id, entity)
			if err != nil {
				t.Fatal(err)
			}
			cached := make(map[string]resultFormContext)
			for k, v := range s.resultForms {
				cached[k] = v
			}
			answer := resultContextEvent(t, s, proc, saved.ResultForm, url.Values{"_element": {target}, "_event": {"Ответ"}, "_question_answer": {"Да"}, "_question_fields": {`{"Примечание":"line1\nline2"}`}}, actor)
			if mode == "valid" || mode == "metadata-command" {
				want := []string{resultContextNonce(saved.ResultForm.Token), "line1\nline2"}
				if !answer.OK || !reflect.DeepEqual(answer.Messages, want) {
					t.Fatalf("answer: %+v want %q", answer, want)
				}
			} else if answer.OK || len(answer.Messages) != 0 {
				t.Fatalf("unsafe continuation: %+v", answer)
			}
			if answer.CloseResultForm || answer.Navigation != nil || answer.ResultForm != nil {
				t.Fatalf("UI side effect: %+v", answer)
			}
			after, err := s.store.GetByID(context.Background(), entity.Name, id, entity)
			if err != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(cached, s.resultForms) {
				t.Fatalf("readonly violation: %v", err)
			}
		})
	}
}
