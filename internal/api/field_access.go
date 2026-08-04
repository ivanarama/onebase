package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// queryMaskPlan is the REST report field gate (план 88E): a protected field in a
// plain projection column comes back masked, one that drives a filter, grouping
// or aggregate denies the query (plan.Denied).
func (h *handler) queryMaskPlan(ctx context.Context, res query.Result) access.QueryMaskPlan {
	return access.QueryMaskPlanFor(auth.UserFromContext(ctx), res, h.sourceMeta)
}

func (h *handler) sourceMeta(kind, name string) *metadata.Entity {
	if e := h.reg.GetEntity(name); e != nil {
		return e
	}
	if r := h.reg.GetRegister(name); r != nil {
		return storage.RegisterPredicateEntity(r)
	}
	if ir := h.reg.GetInfoRegister(name); ir != nil {
		return storage.InfoRegisterPredicateEntity(ir)
	}
	if ar := h.reg.GetAccountRegister(name); ar != nil {
		return storage.AccountRegisterPredicateEntity(ar)
	}
	return nil
}

// fieldDecisions returns the effective per-field masking decisions for reading
// entity as the request user (nil/empty ⇒ nothing to mask).
func (h *handler) fieldDecisions(ctx context.Context, entity *metadata.Entity) map[string]access.FieldDecision {
	if entity == nil {
		return nil
	}
	return access.FieldDecisions(auth.UserFromContext(ctx), string(entity.Kind), entity.Name, entity)
}

// maskRecord applies field masking to a single record in place.
func (h *handler) maskRecord(ctx context.Context, entity *metadata.Entity, row map[string]any) {
	access.MaskRecord(h.fieldDecisions(ctx, entity), row)
}

// maskRecords applies field masking to a list of records in place.
func (h *handler) maskRecords(ctx context.Context, entity *metadata.Entity, rows []map[string]any) {
	access.MaskRecords(h.fieldDecisions(ctx, entity), rows)
}

// protectMaskedFieldsOnWrite restores the real stored value for every field
// masked or hidden for this user before a REST update, mirroring the UI guard —
// a masked user cannot overwrite the real value with the mask or a crafted one.
func (h *handler) protectMaskedFieldsOnWrite(ctx context.Context, entity *metadata.Entity, id uuid.UUID, fields map[string]any) error {
	dec := h.fieldDecisions(ctx, entity)
	if len(dec) == 0 || fields == nil {
		return nil
	}
	row, err := h.store.GetByID(ctx, entity.Name, id, entity)
	if err != nil {
		return err
	}
	for field := range dec {
		key, ok := restCIKey(fields, field)
		if !ok {
			continue
		}
		if v, present := restCIKey2(row, field); present {
			fields[key] = v
		} else {
			delete(fields, key)
		}
	}
	return nil
}

func restCIKey(m map[string]any, field string) (string, bool) {
	for k := range m {
		if strings.EqualFold(k, field) {
			return k, true
		}
	}
	return "", false
}

func restCIKey2(m map[string]any, field string) (any, bool) {
	for k, v := range m {
		if strings.EqualFold(k, field) {
			return v, true
		}
	}
	return nil, false
}

// discloseField serves GET .../{kind}/{name}/{id}/field/{field}. Without
// ?disclose=1 it returns the value as the user would see it (masked). With
// ?disclose=1&reason=… it enforces the object-level `disclose` right, records an
// audit event without the value (план 88, CC-SEC-004) and returns the full value.
func (h *handler) discloseField(kind metadata.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entity, entityName, ok := h.entityFromV2Route(w, r, kind)
		if !ok {
			return
		}
		if !requireRESTPerm(w, r, kind, entityName, "read") {
			return
		}
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id", "", 0)
			return
		}
		field, ok := findEntityField(entity, chi.URLParam(r, "field"))
		if !ok {
			writeError(w, http.StatusNotFound, "unknown field", "", 0)
			return
		}
		row, err := h.store.GetByID(r.Context(), entityName, id, entity)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error(), "", 0)
			return
		}
		if !h.rowAllowed(r.Context(), entity, "read", row) {
			writeError(w, http.StatusForbidden, "forbidden", "", 0)
			return
		}
		full := rowValueCI(row, field.Name)
		dec, masked := h.fieldDecisions(r.Context(), entity)[field.Name]

		if parseReportBool(r.URL.Query().Get("disclose")) {
			reason := strings.TrimSpace(r.URL.Query().Get("reason"))
			if masked {
				if !canREST(r.Context(), string(kind), entityName, "disclose") {
					writeError(w, http.StatusForbidden, "forbidden", "", 0)
					return
				}
				if reason == "" {
					writeError(w, http.StatusBadRequest, "reason required for disclosure", "", 0)
					return
				}
				// Fail-closed (CC-SEC-004): без успешной записи в аудит раскрытие
				// ПДн не выдаём — журнал недоступен ⇒ 500, значение не уходит.
				if err := h.store.LogDisclose(r.Context(), string(kind), entityName, id.String(), field.Name, reason); err != nil {
					writeError(w, http.StatusInternalServerError, "audit failed", "", 0)
					return
				}
			}
			writeJSONV2(w, http.StatusOK, restV2Envelope{Data: map[string]any{
				"field": field.Name, "value": full, "disclosed": masked,
			}})
			return
		}

		value := full
		if masked {
			value = access.MaskValue(dec.Strategy, dec.Keep, full)
		}
		writeJSONV2(w, http.StatusOK, restV2Envelope{Data: map[string]any{
			"field": field.Name, "value": value, "disclosed": false,
		}})
	}
}

func findEntityField(entity *metadata.Entity, raw string) (metadata.Field, bool) {
	if entity == nil {
		return metadata.Field{}, false
	}
	name := raw
	if dec, err := url.PathUnescape(raw); err == nil {
		name = dec
	}
	name = strings.TrimSpace(name)
	for _, f := range entity.Fields {
		if strings.EqualFold(f.Name, name) {
			return f, true
		}
	}
	return metadata.Field{}, false
}

func rowValueCI(row map[string]any, field string) any {
	for k, v := range row {
		if strings.EqualFold(k, field) {
			return v
		}
	}
	return nil
}
