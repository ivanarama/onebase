package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
	"github.com/ivantit66/onebase/internal/storage"
)

type restV2Envelope struct {
	Data any         `json:"data"`
	Meta *restV2Meta `json:"meta,omitempty"`
}

type restV2Meta struct {
	Total      int      `json:"total"`
	Page       int      `json:"page"`
	Limit      int      `json:"limit"`
	TotalPages int      `json:"total_pages"`
	Columns    []string `json:"columns,omitempty"`
	Truncated  bool     `json:"truncated,omitempty"`
	Composed   bool     `json:"composed,omitempty"`
	Variant    string   `json:"variant,omitempty"`
	Kind       string   `json:"kind,omitempty"`
}

type restV2ReportComposition struct {
	Kind   string `json:"kind"`
	Result any    `json:"result"`
}

func (h *handler) mountV2(r chi.Router) {
	r.Route("/api/v2", func(r chi.Router) {
		r.Get("/openapi.json", h.openapiV2())

		r.Get("/catalog/{name}", h.listObjectsV2(metadata.KindCatalog))
		r.Post("/catalog/{name}", h.createObjectV2(metadata.KindCatalog))
		r.Get("/catalog/{name}/{id}", h.getObjectV2(metadata.KindCatalog))
		r.Put("/catalog/{name}/{id}", h.updateObjectV2(metadata.KindCatalog))
		r.Delete("/catalog/{name}/{id}", h.deleteObjectV2(metadata.KindCatalog))

		r.Get("/document/{name}", h.listObjectsV2(metadata.KindDocument))
		r.Post("/document/{name}", h.createObjectV2(metadata.KindDocument))
		r.Get("/document/{name}/{id}", h.getObjectV2(metadata.KindDocument))
		r.Put("/document/{name}/{id}", h.updateObjectV2(metadata.KindDocument))
		r.Delete("/document/{name}/{id}", h.deleteObjectV2(metadata.KindDocument))
		r.Post("/document/{name}/{id}/post", h.postDocumentV2())
		r.Post("/document/{name}/{id}/unpost", h.unpostDocumentV2())

		r.Get("/report/{name}", h.runReportV2())
	})
}

func (h *handler) listObjectsV2(kind metadata.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entity, entityName, ok := h.entityFromV2Route(w, r, kind)
		if !ok {
			return
		}
		if !requireRESTPerm(w, r, kind, entityName, "read") {
			return
		}
		params, page, err := parseRestListParamsV2(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error(), "", 0)
			return
		}
		rows, err := h.store.List(r.Context(), entityName, entity, params)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
			return
		}
		total, err := h.store.CountList(r.Context(), entityName, entity, params)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
			return
		}
		w.Header().Set("X-Total-Count", strconv.Itoa(total))
		w.Header().Set("X-Limit", strconv.Itoa(params.Limit))
		w.Header().Set("X-Offset", strconv.Itoa(params.Offset))
		writeJSONV2(w, http.StatusOK, restV2Envelope{
			Data: rows,
			Meta: &restV2Meta{
				Total:      total,
				Page:       page,
				Limit:      params.Limit,
				TotalPages: totalPages(total, params.Limit),
			},
		})
	}
}

func (h *handler) getObjectV2(kind metadata.Kind) http.HandlerFunc {
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
		result, err := h.store.GetByID(r.Context(), entityName, id, entity)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error(), "", 0)
			return
		}
		writeJSONV2(w, http.StatusOK, restV2Envelope{Data: result})
	}
}

func (h *handler) createObjectV2(kind metadata.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entity, entityName, ok := h.entityFromV2Route(w, r, kind)
		if !ok {
			return
		}
		if !requireRESTPerm(w, r, kind, entityName, "write") {
			return
		}
		limitRESTBody(w, r)
		body, err := decodeBody(r)
		if err != nil {
			writeDecodeError(w, err)
			return
		}
		if kind == metadata.KindDocument && isPostAction(body.Action) && !requireRESTPerm(w, r, kind, entityName, "post") {
			return
		}
		if kind == metadata.KindDocument {
			ensureDocumentNumber(r.Context(), h.store, entity, body.Fields)
		}

		result, err := h.entitySvc.Save(r.Context(), entityservice.SaveRequest{
			Entity:        entity,
			ID:            uuid.New(),
			IsNew:         true,
			Fields:        body.Fields,
			TablePartRows: body.TablePartRows,
			Action:        body.Action,
		})
		writeSaveResultV2(w, result, err, result.ID, false)
	}
}

func (h *handler) updateObjectV2(kind metadata.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entity, entityName, ok := h.entityFromV2Route(w, r, kind)
		if !ok {
			return
		}
		if !requireRESTPerm(w, r, kind, entityName, "write") {
			return
		}
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id", "", 0)
			return
		}
		limitRESTBody(w, r)
		body, err := decodeBody(r)
		if err != nil {
			writeDecodeError(w, err)
			return
		}
		if kind == metadata.KindDocument && isPostAction(body.Action) && !requireRESTPerm(w, r, kind, entityName, "post") {
			return
		}

		var expectedVersion *int64
		if ifMatch := r.Header.Get("If-Match"); ifMatch != "" {
			if v, perr := strconv.ParseInt(strings.Trim(ifMatch, `"`), 10, 64); perr == nil {
				expectedVersion = &v
			}
		}

		result, err := h.entitySvc.Save(r.Context(), entityservice.SaveRequest{
			Entity:          entity,
			ID:              id,
			IsNew:           false,
			Fields:          body.Fields,
			TablePartRows:   body.TablePartRows,
			Action:          body.Action,
			ExpectedVersion: expectedVersion,
		})
		writeSaveResultV2(w, result, err, id, false)
	}
}

func (h *handler) deleteObjectV2(kind metadata.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, entityName, ok := h.entityFromV2Route(w, r, kind)
		if !ok {
			return
		}
		if !requireRESTPerm(w, r, kind, entityName, "delete") {
			return
		}
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id", "", 0)
			return
		}
		if err := h.store.WithTx(r.Context(), func(ctx context.Context) error {
			if kind == metadata.KindDocument {
				if err := h.clearMovements(ctx, entityName, id); err != nil {
					return err
				}
			}
			return h.store.Delete(ctx, entityName, id)
		}); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *handler) postDocumentV2() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entity, entityName, ok := h.entityFromV2Route(w, r, metadata.KindDocument)
		if !ok {
			return
		}
		if !entity.Posting {
			writeError(w, http.StatusBadRequest, "entity is not postable: "+entityName, "", 0)
			return
		}
		if !requireRESTPerm(w, r, metadata.KindDocument, entityName, "post") {
			return
		}
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id", "", 0)
			return
		}

		fields, tpRows, ok := h.documentPostPayload(w, r, entity, entityName, id)
		if !ok {
			return
		}

		result, err := h.entitySvc.Save(r.Context(), entityservice.SaveRequest{
			Entity:        entity,
			ID:            id,
			IsNew:         false,
			Fields:        fields,
			TablePartRows: tpRows,
			Action:        "post",
		})
		writeSaveResultV2(w, result, err, id, true)
	}
}

func (h *handler) unpostDocumentV2() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entity, entityName, ok := h.entityFromV2Route(w, r, metadata.KindDocument)
		if !ok {
			return
		}
		if !entity.Posting {
			writeError(w, http.StatusBadRequest, "entity is not postable: "+entityName, "", 0)
			return
		}
		if !requireRESTPerm(w, r, metadata.KindDocument, entityName, "unpost") {
			return
		}
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id", "", 0)
			return
		}
		if err := h.store.WithTx(r.Context(), func(ctx context.Context) error {
			if err := h.clearMovements(ctx, entityName, id); err != nil {
				return err
			}
			return h.store.SetPosted(ctx, entityName, id, false)
		}); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
			return
		}
		writeJSONV2(w, http.StatusOK, restV2Envelope{Data: map[string]any{
			"id":     id.String(),
			"posted": false,
		}})
	}
}

func (h *handler) runReportV2() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rep, ok := h.reportFromV2Route(w, r)
		if !ok {
			return
		}
		if !canREST(r.Context(), "report", rep.Name, "run") {
			writeError(w, http.StatusForbidden, "forbidden", "", 0)
			return
		}
		limit, err := parsePositiveInt(r.URL.Query().Get("limit"), restDefaultLimit)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid limit", "", 0)
			return
		}
		if limit > restMaxLimit {
			limit = restMaxLimit
		}
		params, err := reportParamsFromQuery(r.URL.Query(), rep)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error(), "", 0)
			return
		}
		compiled, err := query.Compile(rep.Query, query.CompileOpts{
			Entities:    h.reg.Entities(),
			Params:      params,
			Registers:   h.reg.Registers(),
			InfoRegs:    h.reg.InfoRegisters(),
			AccountRegs: h.reg.AccountRegisters(),
			Dialect:     h.store.Dialect(),
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, "query compile error: "+err.Error(), "", 0)
			return
		}
		rows, cols, truncated, err := h.store.RunQueryLimit(r.Context(), compiled.SQL, compiled.Args, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
			return
		}
		if wantsReportComposition(r.URL.Query()) {
			variant := r.URL.Query().Get("variant")
			if variant == "" {
				variant = r.URL.Query().Get("__variant")
			}
			comp := rep.ActiveComposition(variant)
			if comp == nil {
				writeError(w, http.StatusBadRequest, "report has no composition", "", 0)
				return
			}
			kind, data, err := h.composeReportV2(rows, comp)
			if err != nil {
				writeError(w, http.StatusBadRequest, "report composition error: "+err.Error(), "", 0)
				return
			}
			writeJSONV2(w, http.StatusOK, restV2Envelope{
				Data: data,
				Meta: &restV2Meta{
					Total:      len(rows),
					Page:       1,
					Limit:      limit,
					TotalPages: 1,
					Columns:    cols,
					Truncated:  truncated,
					Composed:   true,
					Variant:    variant,
					Kind:       kind,
				},
			})
			return
		}
		writeJSONV2(w, http.StatusOK, restV2Envelope{
			Data: rows,
			Meta: &restV2Meta{
				Total:      len(rows),
				Page:       1,
				Limit:      limit,
				TotalPages: 1,
				Columns:    cols,
				Truncated:  truncated,
			},
		})
	}
}

func (h *handler) composeReportV2(rows []map[string]any, spec *reportpkg.Composition) (string, restV2ReportComposition, error) {
	ev := newReportEvaluator(h.interp)
	if len(spec.Columns) > 0 {
		cr, err := compose.ComposeCross(rows, *spec, ev)
		return "cross", restV2ReportComposition{Kind: "cross", Result: cr}, err
	}
	res, err := compose.Compose(rows, *spec, ev)
	return "tree", restV2ReportComposition{Kind: "tree", Result: res}, err
}

func (h *handler) documentPostPayload(w http.ResponseWriter, r *http.Request, entity *metadata.Entity, entityName string, id uuid.UUID) (map[string]any, map[string][]map[string]any, bool) {
	if r.ContentLength > 0 {
		if !requireRESTPerm(w, r, metadata.KindDocument, entityName, "write") {
			return nil, nil, false
		}
		limitRESTBody(w, r)
		body, err := decodeBody(r)
		if err != nil {
			writeDecodeError(w, err)
			return nil, nil, false
		}
		return body.Fields, body.TablePartRows, true
	}

	fields, err := h.store.GetByID(r.Context(), entityName, id, entity)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error(), "", 0)
		return nil, nil, false
	}
	tpRows := make(map[string][]map[string]any, len(entity.TableParts))
	for _, tp := range entity.TableParts {
		rows, err := h.store.GetTablePartRows(r.Context(), entityName, tp.Name, id, tp)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
			return nil, nil, false
		}
		tpRows[tp.Name] = rows
	}
	return fields, tpRows, true
}

func (h *handler) clearMovements(ctx context.Context, entityName string, id uuid.UUID) error {
	for _, reg := range h.reg.Registers() {
		if err := h.store.WriteMovements(ctx, reg.Name, entityName, id, nil, reg, nil); err != nil {
			return err
		}
	}
	for _, ir := range h.reg.InfoRegisters() {
		if err := h.store.WriteInfoMovements(ctx, ir.Name, entityName, id, nil, ir, nil); err != nil {
			return err
		}
	}
	for _, ar := range h.reg.AccountRegisters() {
		if err := h.store.WriteAccountMovements(ctx, ar.Name, entityName, id, nil, ar, nil); err != nil {
			return err
		}
	}
	return nil
}

func (h *handler) entityFromV2Route(w http.ResponseWriter, r *http.Request, kind metadata.Kind) (*metadata.Entity, string, bool) {
	entityName := capitalize(chi.URLParam(r, "name"))
	entity := h.reg.GetEntity(entityName)
	if entity == nil || entity.Kind != kind {
		writeError(w, http.StatusNotFound, "unknown entity: "+entityName, "", 0)
		return nil, entityName, false
	}
	return entity, entity.Name, true
}

func (h *handler) reportFromV2Route(w http.ResponseWriter, r *http.Request) (*reportpkg.Report, bool) {
	name := chi.URLParam(r, "name")
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	rep := h.reg.GetReport(name)
	if rep == nil {
		writeError(w, http.StatusNotFound, "unknown report: "+name, "", 0)
		return nil, false
	}
	return rep, true
}

func ensureDocumentNumber(ctx context.Context, store *storage.DB, entity *metadata.Entity, fields map[string]any) {
	for _, f := range entity.Fields {
		if f.Name == "Номер" && f.Type == metadata.FieldTypeString {
			if v, _ := fields["Номер"].(string); strings.TrimSpace(v) == "" {
				fields["Номер"] = generateAutoNumber(ctx, store, entity, fields)
			}
			return
		}
	}
}

func writeSaveResultV2(w http.ResponseWriter, result entityservice.SaveResult, err error, id uuid.UUID, posted bool) {
	if err != nil {
		if errors.Is(err, storage.ErrVersionConflict) {
			writeError(w, http.StatusConflict, "version conflict: object was modified by another client", "", 0)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
		return
	}
	if result.DSLError != "" {
		writeError(w, http.StatusUnprocessableEntity, result.DSLError, "", 0)
		return
	}
	data := map[string]any{
		"id":       id.String(),
		"messages": result.DSLMessages,
	}
	if posted {
		data["posted"] = true
	}
	writeJSONV2(w, http.StatusOK, restV2Envelope{Data: data})
}

func writeJSONV2(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func parseRestListParamsV2(r *http.Request) (storage.ListParams, int, error) {
	q := r.URL.Query()
	limit, err := parsePositiveInt(q.Get("limit"), restDefaultLimit)
	if err != nil {
		return storage.ListParams{}, 0, errors.New("invalid limit")
	}
	if limit > restMaxLimit {
		limit = restMaxLimit
	}
	page, err := parsePositiveInt(q.Get("page"), 1)
	if err != nil {
		return storage.ListParams{}, 0, errors.New("invalid page")
	}
	offset := (page - 1) * limit
	if q.Get("offset") != "" {
		offset, err = parseNonNegativeInt(q.Get("offset"), 0)
		if err != nil {
			return storage.ListParams{}, 0, errors.New("invalid offset")
		}
		page = offset/limit + 1
	}
	params := storage.ListParams{
		Filters: parseRestFiltersV2(r),
		Search:  q.Get("q"),
		Limit:   limit,
		Offset:  offset,
	}
	if s := q.Get("sort"); s != "" {
		params.Sort = s
	}
	if d := q.Get("dir"); d != "" {
		params.Dir = d
	}
	return params, page, nil
}

func parseRestFiltersV2(r *http.Request) map[string]storage.FilterValue {
	filters := parseRestFilters(r)
	for k, vals := range r.URL.Query() {
		if len(vals) == 0 || !strings.HasPrefix(k, "filter[") || !strings.HasSuffix(k, "]") {
			continue
		}
		key := strings.TrimSuffix(strings.TrimPrefix(k, "filter["), "]")
		if key == "" {
			continue
		}
		field, attr := splitFilterKey(key)
		fv := filters[field]
		switch attr {
		case "from":
			fv.From = vals[0]
		case "to":
			fv.To = vals[0]
		default:
			fv.Value = vals[0]
		}
		filters[field] = fv
	}
	return filters
}

func splitFilterKey(key string) (string, string) {
	if strings.HasSuffix(key, ".from") {
		return strings.TrimSuffix(key, ".from"), "from"
	}
	if strings.HasSuffix(key, ".to") {
		return strings.TrimSuffix(key, ".to"), "to"
	}
	return key, "value"
}

func totalPages(total, limit int) int {
	if total <= 0 || limit <= 0 {
		return 0
	}
	return (total + limit - 1) / limit
}

func reportParamsFromQuery(q url.Values, rep *reportpkg.Report) (map[string]any, error) {
	params := make(map[string]any, len(rep.Params))
	for _, p := range rep.Params {
		raw := q.Get(p.Name)
		if raw == "" {
			if p.Type == "bool" {
				params[p.Name] = false
			} else {
				params[p.Name] = nil
			}
			continue
		}
		v, err := parseReportParamValue(raw, p.Type)
		if err != nil {
			return nil, fmt.Errorf("invalid report parameter %s: %w", p.Name, err)
		}
		params[p.Name] = v
	}
	return params, nil
}

func parseReportParamValue(raw, typ string) (any, error) {
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "date":
		t, err := time.ParseInLocation("2006-01-02", raw, time.Local)
		if err != nil {
			return nil, errors.New("expected date YYYY-MM-DD")
		}
		return t, nil
	case "bool", "boolean":
		return parseReportBool(raw), nil
	case "number":
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, errors.New("expected number")
		}
		return n, nil
	default:
		return raw, nil
	}
}

func parseReportBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on", "да", "истина":
		return true
	default:
		return false
	}
}

func wantsReportComposition(q url.Values) bool {
	raw := q.Get("composition")
	if raw == "" {
		raw = q.Get("composed")
	}
	return parseReportBool(raw)
}

func (h *handler) openapiV2() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSONV2(w, http.StatusOK, buildOpenAPIV2(h.reg.Entities(), h.reg.Reports()))
	}
}

func buildOpenAPIV2(entities []*metadata.Entity, reports []*reportpkg.Report) map[string]any {
	components := map[string]any{
		"schemas": map[string]any{
			"ListMeta": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"total":       map[string]any{"type": "integer"},
					"page":        map[string]any{"type": "integer"},
					"limit":       map[string]any{"type": "integer"},
					"total_pages": map[string]any{"type": "integer"},
				},
			},
			"ReportMeta": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"total":       map[string]any{"type": "integer"},
					"page":        map[string]any{"type": "integer"},
					"limit":       map[string]any{"type": "integer"},
					"total_pages": map[string]any{"type": "integer"},
					"columns":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"truncated":   map[string]any{"type": "boolean"},
					"composed":    map[string]any{"type": "boolean"},
					"variant":     map[string]any{"type": "string"},
					"kind":        map[string]any{"type": "string", "enum": []string{"tree", "cross"}},
				},
			},
			"ListEnvelope": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"data": map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
					"meta": map[string]any{"$ref": "#/components/schemas/ListMeta"},
				},
			},
			"ObjectEnvelope": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"data": map[string]any{"type": "object"},
				},
			},
			"MutationResult": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":       map[string]any{"type": "string", "format": "uuid"},
					"posted":   map[string]any{"type": "boolean"},
					"messages": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
			},
			"MutationEnvelope": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"data": map[string]any{"$ref": "#/components/schemas/MutationResult"},
				},
			},
			"ReportEnvelope": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"data": map[string]any{"oneOf": []any{
						map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
						map[string]any{"type": "object"},
					}},
					"meta": map[string]any{"$ref": "#/components/schemas/ReportMeta"},
				},
			},
			"Error": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"error": map[string]any{"type": "string"},
					"file":  map[string]any{"type": "string"},
					"line":  map[string]any{"type": "integer"},
				},
			},
		},
		"securitySchemes": map[string]any{
			"sessionCookie": map[string]any{"type": "apiKey", "in": "cookie", "name": "onebase_session"},
			"bearerAuth":    map[string]any{"type": "http", "scheme": "bearer"},
		},
	}
	schemas := components["schemas"].(map[string]any)
	for _, e := range entities {
		schemas[entitySchemaName(e)] = entityOpenAPISchema(e)
	}
	for _, rep := range reports {
		schemas[reportSchemaName(rep)] = reportOpenAPISchema(rep)
	}

	doc := map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]any{"title": "onebase REST API", "version": "v2"},
		"servers": []any{map[string]any{"url": "/"}},
		"security": []any{
			map[string]any{"sessionCookie": []any{}},
			map[string]any{"bearerAuth": []any{}},
		},
		"paths":      openAPIV2Paths(),
		"components": components,
	}
	return doc
}

func openAPIV2Paths() map[string]any {
	nameParam := map[string]any{"name": "name", "in": "path", "required": true, "schema": map[string]any{"type": "string"}}
	idParam := map[string]any{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string", "format": "uuid"}}
	listParams := []any{
		nameParam,
		map[string]any{"name": "q", "in": "query", "schema": map[string]any{"type": "string"}},
		map[string]any{"name": "page", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 1}},
		map[string]any{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 1, "maximum": restMaxLimit}},
		map[string]any{"name": "sort", "in": "query", "schema": map[string]any{"type": "string"}},
		map[string]any{"name": "dir", "in": "query", "schema": map[string]any{"type": "string", "enum": []string{"asc", "desc"}}},
		map[string]any{"name": "filter[Field]", "in": "query", "schema": map[string]any{"type": "string"}},
	}
	jsonBody := map[string]any{
		"required": false,
		"content": map[string]any{
			"application/json": map[string]any{"schema": map[string]any{"type": "object"}},
		},
	}
	okEnvelope := map[string]any{
		"description": "OK",
		"content": map[string]any{
			"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/ObjectEnvelope"}},
		},
	}
	openAPIResponse := map[string]any{
		"description": "OK",
		"content": map[string]any{
			"application/json": map[string]any{"schema": map[string]any{"type": "object"}},
		},
	}
	listEnvelope := responseWithSchema("OK", "#/components/schemas/ListEnvelope")
	mutationEnvelope := responseWithSchema("OK", "#/components/schemas/MutationEnvelope")
	reportEnvelope := responseWithSchema("OK", "#/components/schemas/ReportEnvelope")
	errorResponses := map[string]any{
		"400": responseWithSchema("Bad Request", "#/components/schemas/Error"),
		"403": responseWithSchema("Forbidden", "#/components/schemas/Error"),
		"404": responseWithSchema("Not Found", "#/components/schemas/Error"),
		"500": responseWithSchema("Internal Server Error", "#/components/schemas/Error"),
	}
	noContent := map[string]any{"description": "No Content"}
	crud := func(tag string) map[string]any {
		return map[string]any{
			"get": map[string]any{
				"operationId": "list" + titleAPIName(tag),
				"summary":     "List " + tag,
				"tags":        []string{tag},
				"parameters":  listParams,
				"responses":   mergeResponses(map[string]any{"200": listEnvelope}, errorResponses),
			},
			"post": map[string]any{
				"operationId": "create" + titleAPIName(tag),
				"summary":     "Create " + tag,
				"tags":        []string{tag},
				"parameters":  []any{nameParam},
				"requestBody": jsonBody,
				"responses":   mergeResponses(map[string]any{"200": mutationEnvelope}, errorResponses),
			},
		}
	}
	item := func(tag string) map[string]any {
		return map[string]any{
			"get": map[string]any{
				"operationId": "get" + titleAPIName(tag),
				"summary":     "Get " + tag,
				"tags":        []string{tag},
				"parameters":  []any{nameParam, idParam},
				"responses":   mergeResponses(map[string]any{"200": okEnvelope}, errorResponses),
			},
			"put": map[string]any{
				"operationId": "update" + titleAPIName(tag),
				"summary":     "Update " + tag,
				"tags":        []string{tag},
				"parameters":  []any{nameParam, idParam},
				"requestBody": jsonBody,
				"responses":   mergeResponses(map[string]any{"200": mutationEnvelope}, errorResponses),
			},
			"delete": map[string]any{
				"operationId": "delete" + titleAPIName(tag),
				"summary":     "Delete " + tag,
				"tags":        []string{tag},
				"parameters":  []any{nameParam, idParam},
				"responses":   mergeResponses(map[string]any{"204": noContent}, errorResponses),
			},
		}
	}
	return map[string]any{
		"/api/v2/catalog/{name}":              crud("catalog"),
		"/api/v2/catalog/{name}/{id}":         item("catalog"),
		"/api/v2/document/{name}":             crud("document"),
		"/api/v2/document/{name}/{id}":        item("document"),
		"/api/v2/document/{name}/{id}/post":   actionPath("postDocument", "Post document", nameParam, idParam, mutationEnvelope, errorResponses),
		"/api/v2/document/{name}/{id}/unpost": actionPath("unpostDocument", "Unpost document", nameParam, idParam, mutationEnvelope, errorResponses),
		"/api/v2/report/{name}":               reportPath(nameParam, reportEnvelope, errorResponses),
		"/api/v2/openapi.json": map[string]any{
			"get": map[string]any{
				"operationId": "getOpenAPI",
				"summary":     "OpenAPI document",
				"tags":        []string{"openapi"},
				"responses":   mergeResponses(map[string]any{"200": openAPIResponse}, errorResponses),
			},
		},
	}
}

func actionPath(operationID, summary string, nameParam, idParam, okEnvelope map[string]any, errors map[string]any) map[string]any {
	return map[string]any{
		"post": map[string]any{
			"operationId": operationID,
			"summary":     summary,
			"tags":        []string{"document"},
			"parameters":  []any{nameParam, idParam},
			"responses":   mergeResponses(map[string]any{"200": okEnvelope}, errors),
		},
	}
}

func reportPath(nameParam, okEnvelope map[string]any, errors map[string]any) map[string]any {
	limitParam := map[string]any{
		"name":        "limit",
		"in":          "query",
		"description": "Maximum rows to return; report parameters are also passed as query parameters by name.",
		"schema":      map[string]any{"type": "integer", "minimum": 1, "maximum": restMaxLimit},
	}
	compositionParam := map[string]any{
		"name":        "composition",
		"in":          "query",
		"description": "Set to 1/true to return the YAML report composition instead of a flat row array.",
		"schema":      map[string]any{"type": "boolean"},
	}
	variantParam := map[string]any{
		"name":        "variant",
		"in":          "query",
		"description": "Report composition variant name. Empty or unknown falls back to the base composition.",
		"schema":      map[string]any{"type": "string"},
	}
	return map[string]any{
		"get": map[string]any{
			"operationId": "runReport",
			"summary":     "Run report",
			"tags":        []string{"report"},
			"parameters":  []any{nameParam, limitParam, compositionParam, variantParam},
			"responses":   mergeResponses(map[string]any{"200": okEnvelope}, errors),
		},
	}
}

func responseWithSchema(description, ref string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{"schema": map[string]any{"$ref": ref}},
		},
	}
}

func mergeResponses(base map[string]any, extras map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extras))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extras {
		out[k] = v
	}
	return out
}

func titleAPIName(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func entityOpenAPISchema(e *metadata.Entity) map[string]any {
	props := map[string]any{
		"id":            map[string]any{"type": "string", "format": "uuid"},
		"deletion_mark": map[string]any{"type": "boolean"},
	}
	if e.Kind == metadata.KindDocument {
		props["posted"] = map[string]any{"type": "boolean"}
	}
	for _, f := range e.Fields {
		props[f.Name] = fieldOpenAPISchema(f)
	}
	if len(e.TableParts) > 0 {
		tpProps := map[string]any{}
		for _, tp := range e.TableParts {
			rowProps := map[string]any{}
			for _, f := range tp.Fields {
				rowProps[f.Name] = fieldOpenAPISchema(f)
			}
			tpProps[tp.Name] = map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "object", "properties": rowProps},
			}
		}
		props["__tableparts"] = map[string]any{"type": "object", "properties": tpProps}
	}
	return map[string]any{"type": "object", "properties": props}
}

func fieldOpenAPISchema(f metadata.Field) map[string]any {
	if f.RefEntity != "" || metadata.IsReference(f.Type) {
		return map[string]any{"type": "string", "format": "uuid"}
	}
	if f.EnumName != "" || metadata.IsEnum(f.Type) {
		return map[string]any{"type": "string"}
	}
	switch f.Type {
	case metadata.FieldTypeDate:
		return map[string]any{"type": "string", "format": "date-time"}
	case metadata.FieldTypeNumber:
		return map[string]any{"type": "number"}
	case metadata.FieldTypeBool:
		return map[string]any{"type": "boolean"}
	case metadata.FieldTypeImage:
		return map[string]any{"type": "string", "format": "uuid"}
	default:
		return map[string]any{"type": "string"}
	}
}

func entitySchemaName(e *metadata.Entity) string {
	return string(e.Kind) + "_" + e.Name
}

func reportOpenAPISchema(rep *reportpkg.Report) map[string]any {
	props := map[string]any{}
	for _, p := range rep.Params {
		props[p.Name] = reportParamOpenAPISchema(p)
	}
	return map[string]any{"type": "object", "properties": props}
}

func reportParamOpenAPISchema(p reportpkg.Param) map[string]any {
	switch strings.ToLower(p.Type) {
	case "date":
		return map[string]any{"type": "string", "format": "date"}
	case "number":
		return map[string]any{"type": "number"}
	case "bool", "boolean":
		return map[string]any{"type": "boolean"}
	case "select":
		return map[string]any{"type": "string", "enum": p.Options}
	default:
		if strings.HasPrefix(strings.ToLower(p.Type), "reference:") {
			return map[string]any{"type": "string", "format": "uuid"}
		}
		return map[string]any{"type": "string"}
	}
}

func reportSchemaName(rep *reportpkg.Report) string {
	return "report_" + rep.Name
}
