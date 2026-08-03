package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// fieldDecisions returns the effective per-field masking decisions for reading
// entity as the request user (nil/empty ⇒ nothing to mask).
func (s *Server) fieldDecisions(ctx context.Context, entity *metadata.Entity) map[string]access.FieldDecision {
	if entity == nil {
		return nil
	}
	return access.FieldDecisions(auth.UserFromContext(ctx), string(entity.Kind), entity.Name, entity)
}

// maskRecord masks/hides sensitive fields of one record in place before it is
// rendered or serialised. Shared chokepoint for every UI read path.
func (s *Server) maskRecord(ctx context.Context, entity *metadata.Entity, row map[string]any) {
	access.MaskRecord(s.fieldDecisions(ctx, entity), row)
}

// maskRecords masks a list of records in place.
func (s *Server) maskRecords(ctx context.Context, entity *metadata.Entity, rows []map[string]any) {
	access.MaskRecords(s.fieldDecisions(ctx, entity), rows)
}

// maskedRecordLabel is the only safe way to derive a user-visible label from
// a freshly loaded record. Reference resolvers often read a row vertically
// (UUID → label), bypassing the list/form masking chokepoints; mask first so a
// sensitive first string field cannot leak through reports, widgets or audit.
func (s *Server) maskedRecordLabel(ctx context.Context, entity *metadata.Entity, row map[string]any) string {
	s.maskRecord(ctx, entity, row)
	return firstStringField(row, entity)
}

// deniedMaskedColumn is the fail-closed report/AI gate (план 88D): cols are
// logical fields from query.Result.ProjectionFields, before output aliases.
func (s *Server) deniedMaskedColumn(ctx context.Context, sources []query.SourceRef, cols []string) string {
	return access.DeniedMaskedColumn(auth.UserFromContext(ctx), sources, cols, s.sourceMeta)
}

// sourceMeta resolves the metadata of a query source object (entity/register/
// inforeg/account register); nil if unknown.
func (s *Server) sourceMeta(kind, name string) *metadata.Entity {
	if e := s.reg.GetEntity(name); e != nil {
		return e
	}
	if r := s.reg.GetRegister(name); r != nil {
		return storage.RegisterPredicateEntity(r)
	}
	if ir := s.reg.GetInfoRegister(name); ir != nil {
		return storage.InfoRegisterPredicateEntity(ir)
	}
	if ar := s.reg.GetAccountRegister(name); ar != nil {
		return storage.AccountRegisterPredicateEntity(ar)
	}
	return nil
}

// protectMaskedFieldsOnWrite restores the real stored value for any field masked
// or hidden for this user before an update, so a user who only ever saw the mask
// cannot overwrite the real value — neither with the mask itself nor with a
// crafted request. Consistent with «нельзя изменить то, что не видно». Applied on
// update only; on create the user legitimately enters their own values.
func (s *Server) protectMaskedFieldsOnWrite(ctx context.Context, entity *metadata.Entity, id uuid.UUID, fields map[string]any) error {
	dec := s.fieldDecisions(ctx, entity)
	if len(dec) == 0 || fields == nil {
		return nil
	}
	row, err := s.store.GetByID(ctx, entity.Name, id, entity)
	if err != nil {
		return err
	}
	for field := range dec {
		key, ok := maskCIKey(fields, field)
		if !ok {
			continue // field not submitted → nothing to overwrite
		}
		if v, present := maskCIKeyValue(row, field); present {
			fields[key] = v
		} else {
			delete(fields, key)
		}
	}
	return nil
}

// discloseField serves POST /ui/{kind}/{entity}/{id}/disclose with form fields
// {field, reason}. It enforces the object-level `disclose` right, records an
// audit event without the value (план 88, CC-SEC-004) and returns the full value
// inline as JSON so the form can reveal it without a reload.
func (s *Server) discloseField(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	if !s.requirePerm(w, r, string(entity.Kind), entity.Name, "read") {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	field, ok := entityFieldByName(entity, r.FormValue("field"))
	if !ok {
		http.Error(w, "unknown field", http.StatusNotFound)
		return
	}
	row, err := s.store.GetByID(r.Context(), entity.Name, id, entity)
	if err != nil {
		http.Error(w, s.errText(r, err), http.StatusNotFound)
		return
	}
	if !s.rowAllowed(w, r, entity, "read", row) {
		return // rowAllowed already rendered 403
	}
	_, masked := s.fieldDecisions(r.Context(), entity)[field.Name]
	if masked {
		if !s.can(r, string(entity.Kind), entity.Name, "disclose") {
			s.renderForbidden(w, r)
			return
		}
		reason := strings.TrimSpace(r.FormValue("reason"))
		if reason == "" {
			http.Error(w, "reason required", http.StatusBadRequest)
			return
		}
		// Fail-closed (CC-SEC-004): раскрытие ПДн без успешной записи в аудит
		// недопустимо — если журнал недоступен, значение клиенту не выдаём.
		if err := s.store.LogDisclose(r.Context(), string(entity.Kind), entity.Name, id.String(), field.Name, reason); err != nil {
			http.Error(w, "audit failed", http.StatusInternalServerError)
			return
		}
	}
	full, _ := maskCIKeyValue(row, field.Name)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"field":     field.Name,
		"value":     full,
		"disclosed": masked,
	})
}

func entityFieldByName(entity *metadata.Entity, name string) (metadata.Field, bool) {
	name = strings.TrimSpace(name)
	if entity == nil || name == "" {
		return metadata.Field{}, false
	}
	for _, f := range entity.Fields {
		if strings.EqualFold(f.Name, name) {
			return f, true
		}
	}
	return metadata.Field{}, false
}

func maskCIKey(m map[string]any, field string) (string, bool) {
	for k := range m {
		if strings.EqualFold(k, field) {
			return k, true
		}
	}
	return "", false
}

func maskCIKeyValue(m map[string]any, field string) (any, bool) {
	for k, v := range m {
		if strings.EqualFold(k, field) {
			return v, true
		}
	}
	return nil, false
}
