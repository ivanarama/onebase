package ui

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

const resultContextRefusal = "КонтекстФормыРезультата доступен только в команде текущей формы результата"

// resultContextNonce is an identity for journaling/retries, never authorization.
// A domain-separated digest cannot reveal the random bearer. UUIDv8 also cannot
// equal an existing UUIDv4 bearer. No state is written, even on the first read.
func resultContextNonce(token string) string {
	digest := sha256.Sum256([]byte("onebase.result-context.v1\x00" + token))
	var id uuid.UUID
	copy(id[:], digest[:16])
	id[6] = (id[6] & 0x0f) | 0x80
	id[8] = (id[8] & 0x3f) | 0x80
	return id.String()
}

func resultContextCommandAllowed(target browserFormEventTarget, event string, closing bool) bool {
	if closing || (event != string(metadata.FormEventOnClick) && event != string(metadata.FormEventOnAnswer)) {
		return false
	}
	// A metadata command's Action is the declared handler for both phases.
	// Buttons have separate handlers: an Answer-only button is not a result
	// command continuation. A posted _question_answer grants no authority.
	if target.command != nil {
		return strings.TrimSpace(target.command.Action) != ""
	}
	el := target.element
	if el == nil || el.Kind != metadata.FormElementButton || strings.TrimSpace(el.Handlers[metadata.FormEventOnClick]) == "" {
		return false
	}
	return event == string(metadata.FormEventOnClick) || strings.TrimSpace(el.Handlers[metadata.FormEventOnAnswer]) != ""
}

func (s *Server) resultContextBuiltin(r *http.Request, processorName string, allowed bool) interpreter.BuiltinFunc {
	return func(args []any, _ string, _ int) (any, error) {
		deny := func() (any, error) { return nil, fmt.Errorf("%s", s.tr(s.resolveLang(r), resultContextRefusal)) }
		if !allowed || len(args) != 0 {
			return deny()
		}
		u := auth.UserFromContext(r.Context())
		tokens := r.URL.Query()[resultContextQuery]
		if u == nil || u.ID == "" || u.Login == "" || len(tokens) != 1 || tokens[0] == "" {
			return deny()
		}
		c, err := s.readResultForm(r, processorName)
		if err != nil || c == nil || time.Now().After(c.expires) {
			return deny()
		}
		// Legacy processors may silently pin a forged parameter back to the
		// server binding. This guard refuses it before a new worker can start.
		if values, present := processorPostFormValues(r, c.request.parameter); present {
			if len(values) != 1 {
				return deny()
			}
			posted, err := uuid.Parse(strings.TrimSpace(values[0]))
			bound, boundErr := uuid.Parse(c.request.reference.UUID)
			if err != nil || boundErr != nil || posted != bound {
				return deny()
			}
		}
		proc := s.reg.GetProcessor(processorName)
		entity := s.reg.GetEntity(c.request.reference.Type)
		boundID, parseErr := uuid.Parse(c.request.reference.UUID)
		if proc == nil || entity == nil || s.store == nil || parseErr != nil || boundID == uuid.Nil {
			return deny()
		}
		// An unrestricted/admin permission decision alone does not prove that
		// the record still exists. Read only its ID, with the current RLS in SQL.
		params, filterErr := s.rowFilterFor(r.Context(), entity, "read", storage.ListParams{})
		if filterErr != nil {
			return deny()
		}
		rows, readErr := s.store.GetFieldsByIDsFiltered(r.Context(), entity, []uuid.UUID{boundID}, nil, params.RowFilter)
		if readErr != nil || rows[boundID.String()] == nil || time.Now().After(c.expires) {
			return deny()
		}
		// Copies have no manager/resolver hooks: merely reading the guard cannot
		// write or perform an extra lazy attribute lookup through the reference.
		ref := &interpreter.Ref{Kind: entity.Kind, Type: entity.Name,
			UUID: boundID.String(), Name: boundID.String()}
		actor := interpreter.NewStructFromMap(map[string]any{"Имя": u.Login, "ИД": u.ID})
		return interpreter.NewStructFromMap(map[string]any{
			"Пользователь": actor, "Обработка": proc.Name, "Ссылка": ref,
			"ИДКонтекста": resultContextNonce(tokens[0]),
		}), nil
	}
}
