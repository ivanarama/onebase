package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

type handler struct {
	reg    *runtime.Registry
	store  *storage.DB
	interp *interpreter.Interpreter
}

func (h *handler) createObject(kind metadata.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entityName := chi.URLParam(r, "entity")
		// capitalize first letter to match registered entity names
		if len(entityName) > 0 {
			entityName = capitalize(entityName)
		}
		entity := h.reg.GetEntity(entityName)
		if entity == nil {
			writeError(w, http.StatusNotFound, "unknown entity: "+entityName, "", 0)
			return
		}

		var fields map[string]any
		if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "", 0)
			return
		}

		obj := runtime.NewObject(entityName, kind)
		for k, v := range fields {
			obj.Set(k, v)
		}

		// run OnWrite if defined
		proc := h.reg.GetProcedure(entityName, "OnWrite")
		if proc != nil {
			if err := h.interp.Run(proc, obj); err != nil {
				if dslErr, ok := err.(*interpreter.DSLError); ok {
					writeError(w, http.StatusUnprocessableEntity, dslErr.Msg, dslErr.File, dslErr.Line)
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
				return
			}
		}

		if err := h.store.Upsert(r.Context(), entityName, obj.ID, obj.Fields, entity); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"id": obj.ID.String()})
	}
}

func (h *handler) getObject(kind metadata.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entityName := capitalize(chi.URLParam(r, "entity"))
		entity := h.reg.GetEntity(entityName)
		if entity == nil {
			writeError(w, http.StatusNotFound, "unknown entity: "+entityName, "", 0)
			return
		}
		idStr := chi.URLParam(r, "id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id", "", 0)
			return
		}
		result, err := h.store.GetByID(r.Context(), entityName, id, entity)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error(), "", 0)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

func (h *handler) listObjects(kind metadata.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entityName := capitalize(chi.URLParam(r, "entity"))
		entity := h.reg.GetEntity(entityName)
		if entity == nil {
			writeError(w, http.StatusNotFound, "unknown entity: "+entityName, "", 0)
			return
		}
		params := storage.ListParams{Filters: parseRestFilters(r)}
		if s := r.URL.Query().Get("sort"); s != "" {
			params.Sort = s
		}
		if d := r.URL.Query().Get("dir"); d != "" {
			params.Dir = d
		}
		rows, err := h.store.List(r.Context(), entityName, entity, params)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rows)
	}
}

func (h *handler) updateObject(kind metadata.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entityName := capitalize(chi.URLParam(r, "entity"))
		entity := h.reg.GetEntity(entityName)
		if entity == nil {
			writeError(w, http.StatusNotFound, "unknown entity: "+entityName, "", 0)
			return
		}
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id", "", 0)
			return
		}
		var fields map[string]any
		if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "", 0)
			return
		}
		obj := runtime.NewObject(entityName, kind)
		obj.ID = id
		for k, v := range fields {
			obj.Set(k, v)
		}
		proc := h.reg.GetProcedure(entityName, "OnWrite")
		if proc != nil {
			if err := h.interp.Run(proc, obj); err != nil {
				if dslErr, ok := err.(*interpreter.DSLError); ok {
					writeError(w, http.StatusUnprocessableEntity, dslErr.Msg, dslErr.File, dslErr.Line)
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
				return
			}
		}
		if err := h.store.Upsert(r.Context(), entityName, id, obj.Fields, entity); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": id.String()})
	}
}

func (h *handler) deleteObject(kind metadata.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entityName := capitalize(chi.URLParam(r, "entity"))
		if h.reg.GetEntity(entityName) == nil {
			writeError(w, http.StatusNotFound, "unknown entity: "+entityName, "", 0)
			return
		}
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id", "", 0)
			return
		}
		if err := h.store.WithTx(r.Context(), func(ctx context.Context) error {
			// Clear movements for documents before deleting
			if kind == metadata.KindDocument {
				for _, reg := range h.reg.Registers() {
					if err := h.store.WriteMovements(ctx, reg.Name, entityName, id, nil, reg, nil); err != nil {
						return err
					}
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

func parseRestFilters(r *http.Request) map[string]storage.FilterValue {
	filters := make(map[string]storage.FilterValue)
	for k, vals := range r.URL.Query() {
		if strings.HasPrefix(k, "f.") && len(vals) > 0 {
			filters[strings.TrimPrefix(k, "f.")] = storage.FilterValue{Value: vals[0]}
		}
	}
	return filters
}

type errorResponse struct {
	Error string `json:"error"`
	File  string `json:"file,omitempty"`
	Line  int    `json:"line,omitempty"`
}

func writeError(w http.ResponseWriter, code int, msg, file string, line int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(errorResponse{Error: msg, File: file, Line: line})
}

func capitalize(s string) string {
	if dec, err := url.PathUnescape(s); err == nil {
		s = dec
	}
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
	return string(runes)
}
