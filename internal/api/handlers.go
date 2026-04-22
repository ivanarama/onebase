package api

import (
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
