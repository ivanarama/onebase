package ui

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/widget"
)

const resultContextQuery = "_ob_result_context"

func processorResultCloseRoute(r *http.Request, name string) string {
	key := "processor|" + strings.ToLower(name)
	if token := r.URL.Query().Get(resultContextQuery); token != "" {
		key += "|result:" + token
	}
	return key
}

func (s *Server) releaseResultForm(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	s.resultFormMu.Lock()
	if c, ok := s.resultForms[token]; ok && c.owner == resultFormOwner(r) {
		delete(s.resultForms, token)
	}
	s.resultFormMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

type resultFormRequest struct {
	processor string
	parameter string
	reference interpreter.Ref
}

type resultFormContext struct {
	request resultFormRequest
	owner   string
	expires time.Time
}

type resultFormPayload struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// An explicit result handoff consumes transient scalar display state. It does
// not relax the ordinary dirty contract, durable fields, service state or TPs.
func resultHandoffForm(entity *metadata.Entity, form *metadata.FormModule) *metadata.FormModule {
	if form == nil {
		return form
	}
	view := *form
	view.Attributes = nil
	for _, attr := range form.Attributes {
		if attr != nil && !attr.Save && !attr.MainAttribute && formAttrIsScalar(attr) &&
			entityField(entity, attr.Name) == nil && !entityServiceFieldName(attr.Name) {
			continue
		}
		view.Attributes = append(view.Attributes, attr)
	}
	return &view
}

func resultFormOwner(r *http.Request) string {
	return formCloseAdmissionIdentity(r, formCloseIdentity(r))
}

// The builtin records intent. Nothing is published until the handler has
// completed, its transaction has committed, and its form is known to be clean.
func newResultFormBuiltin(sink **resultFormRequest, allowed bool) interpreter.BuiltinFunc {
	return func(args []any, _ string, _ int) (any, error) {
		if !allowed || len(args) != 3 {
			return nil, fmt.Errorf("ОткрытьФормуРезультата(Обработка, Ссылка, Параметр) доступна только в команде карточки")
		}
		name, nameOK := args[0].(string)
		ref, refOK := args[1].(*interpreter.Ref)
		parameter, paramOK := args[2].(string)
		if !nameOK || !paramOK || !refOK || ref == nil || strings.TrimSpace(name) == "" || strings.TrimSpace(parameter) == "" {
			return nil, fmt.Errorf("ОткрытьФормуРезультата: нужны имя обработки, типизированная ссылка и имя параметра")
		}
		if _, err := uuid.Parse(ref.UUID); err != nil {
			return nil, fmt.Errorf("ОткрытьФормуРезультата: запись должна быть сохранена")
		}
		*sink = &resultFormRequest{processor: name, parameter: parameter, reference: *ref}
		return nil, nil
	}
}

func (s *Server) resultFormAllowed(r *http.Request, request resultFormRequest) (*processor.Processor, error) {
	proc := s.reg.GetProcessor(request.processor)
	entity := s.reg.GetEntity(request.reference.Type)
	if proc == nil || proc.ManagedForm() == nil || entity == nil || entity.Kind != request.reference.Kind ||
		!s.can(r, "processor", proc.Name, "run") || !s.canRunExternalProc(r, proc) {
		return nil, fmt.Errorf("форма результата недоступна")
	}
	validParam := false
	for _, p := range proc.Params {
		if p.Name == request.parameter && strings.EqualFold(p.Type, "reference:"+entity.Name) {
			validParam = true
		}
	}
	checker := widget.New(s.reg, s.store)
	checker.User = auth.UserFromContext(r.Context())
	if !validParam || !checker.ReferenceAllowed(r.Context(), entity.Name, request.reference.UUID) {
		return nil, fmt.Errorf("форма результата недоступна")
	}
	return proc, nil
}

func (s *Server) prepareResultForm(r *http.Request, request *resultFormRequest, source *formObjectThis, dirty bool) (*resultFormPayload, error) {
	if request == nil {
		return nil, nil
	}
	if source == nil || !source.saved || dirty || source.obj == nil || source.obj.ID.String() != request.reference.UUID ||
		!strings.EqualFold(source.entity.Name, request.reference.Type) {
		return nil, fmt.Errorf("ОткрытьФормуРезультата: исходная карточка должна быть успешно записана без несохранённых изменений")
	}
	proc, err := s.resultFormAllowed(r, *request)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	owner := resultFormOwner(r)
	token := uuid.NewString()
	s.resultFormMu.Lock()
	defer s.resultFormMu.Unlock()
	if s.resultForms == nil {
		s.resultForms = make(map[string]resultFormContext)
	}
	owned := 0
	for key, c := range s.resultForms {
		if now.After(c.expires) {
			delete(s.resultForms, key)
		} else if c.owner == owner {
			owned++
		}
	}
	if owned >= 32 || len(s.resultForms) >= 2048 {
		return nil, fmt.Errorf("слишком много открытых контекстов результата")
	}
	s.resultForms[token] = resultFormContext{request: *request, owner: owner, expires: now.Add(8 * time.Hour)}
	return &resultFormPayload{Token: token, URL: "/ui/processor/" + url.PathEscape(proc.Name) + "?" + resultContextQuery + "=" + token}, nil
}

// The opaque context is rechecked on GET, every command, and close-intent.
// Its record identity is never taken from editable browser fields.
func (s *Server) readResultForm(r *http.Request, processorName string) (*resultFormContext, error) {
	token := r.URL.Query().Get(resultContextQuery)
	if token == "" {
		return nil, nil
	}
	s.resultFormMu.Lock()
	c, exists := s.resultForms[token]
	s.resultFormMu.Unlock()
	if !exists || c.owner != resultFormOwner(r) || time.Now().After(c.expires) || !strings.EqualFold(c.request.processor, processorName) {
		return nil, fmt.Errorf("контекст результата недоступен или истёк")
	}
	if _, err := s.resultFormAllowed(r, c.request); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Server) applyResultFormParameter(r *http.Request, processorName string, values map[string]any) error {
	c, err := s.readResultForm(r, processorName)
	if err != nil || c == nil {
		return err
	}
	// The posted identity may be absent (unplaced/read-only), but must not
	// replace the server binding even if a hand-written request submits it.
	for name, value := range values {
		if strings.EqualFold(name, c.request.parameter) {
			if fmt.Sprint(value) != c.request.reference.UUID && value != nil {
				return fmt.Errorf("параметр ссылки формы результата неизменяем")
			}
			delete(values, name)
		}
	}
	values[c.request.parameter] = c.request.reference.UUID
	return nil
}

func (s *Server) resultCloseBuiltin(r *http.Request, processorName string, sink *bool, allowed bool) interpreter.BuiltinFunc {
	return func(args []any, _ string, _ int) (any, error) {
		c, err := s.readResultForm(r, processorName)
		if err != nil || c == nil || !allowed || len(args) != 0 {
			return nil, fmt.Errorf("ЗакрытьФормуРезультата доступна только в команде текущей формы результата")
		}
		*sink = true
		return nil, nil
	}
}
