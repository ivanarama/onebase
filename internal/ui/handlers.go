package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	s.render(w, "page-index", map[string]any{"Nav": s.buildNav()})
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	rows, err := s.store.List(r.Context(), entity.Name, entity)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.resolveRefs(r.Context(), entity, rows)
	s.render(w, "page-list", map[string]any{
		"Nav":    s.buildNav(),
		"Entity": entity,
		"Rows":   rows,
	})
}

func (s *Server) form(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	refOptions, _ := s.loadRefOptions(r.Context(), entity)
	s.render(w, "page-form", map[string]any{
		"Nav":        s.buildNav(),
		"Entity":     entity,
		"IsNew":      true,
		"Values":     map[string]string{},
		"RefOptions": refOptions,
	})
}

func (s *Server) submit(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	fields := formToFields(r, entity)
	obj := runtime.NewObject(entity.Name, entity.Kind)
	for k, v := range fields {
		obj.Set(k, v)
	}

	if errMsg := s.runOnWrite(obj); errMsg != "" {
		refOptions, _ := s.loadRefOptions(r.Context(), entity)
		s.render(w, "page-form", map[string]any{
			"Nav":        s.buildNav(),
			"Entity":     entity,
			"IsNew":      true,
			"Error":      errMsg,
			"Values":     formValues(r, entity),
			"RefOptions": refOptions,
		})
		return
	}

	if err := s.store.Upsert(r.Context(), entity.Name, obj.ID, obj.Fields, entity); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, listURL(entity), http.StatusSeeOther)
}

func (s *Server) formEdit(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	row, err := s.store.GetByID(r.Context(), entity.Name, id, entity)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	refOptions, _ := s.loadRefOptions(r.Context(), entity)
	vals := make(map[string]string)
	for _, f := range entity.Fields {
		if v := row[f.Name]; v != nil {
			vals[f.Name] = fmt.Sprintf("%v", v)
		}
	}
	s.render(w, "page-form", map[string]any{
		"Nav":        s.buildNav(),
		"Entity":     entity,
		"IsNew":      false,
		"Values":     vals,
		"RefOptions": refOptions,
	})
}

func (s *Server) submitEdit(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	fields := formToFields(r, entity)
	obj := &runtime.Object{Type: entity.Name, Kind: entity.Kind, ID: id, Fields: fields}

	if errMsg := s.runOnWrite(obj); errMsg != "" {
		refOptions, _ := s.loadRefOptions(r.Context(), entity)
		s.render(w, "page-form", map[string]any{
			"Nav":        s.buildNav(),
			"Entity":     entity,
			"IsNew":      false,
			"Error":      errMsg,
			"Values":     formValues(r, entity),
			"RefOptions": refOptions,
		})
		return
	}

	if err := s.store.Upsert(r.Context(), entity.Name, obj.ID, obj.Fields, entity); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, listURL(entity), http.StatusSeeOther)
}

func (s *Server) runOnWrite(obj *runtime.Object) string {
	proc := s.reg.GetProcedure(obj.Type, "OnWrite")
	if proc == nil {
		return ""
	}
	if err := s.interp.Run(proc, obj); err != nil {
		if dslErr, ok := err.(*interpreter.DSLError); ok {
			return dslErr.Msg
		}
		return err.Error()
	}
	return ""
}

func (s *Server) getEntity(w http.ResponseWriter, r *http.Request) *metadata.Entity {
	name := capitalize(chi.URLParam(r, "entity"))
	e := s.reg.GetEntity(name)
	if e == nil {
		http.Error(w, "unknown entity: "+name, 404)
		return nil
	}
	return e
}

func (s *Server) loadRefOptions(ctx context.Context, entity *metadata.Entity) (map[string][]map[string]any, error) {
	opts := make(map[string][]map[string]any)
	for _, f := range entity.Fields {
		if f.RefEntity == "" {
			continue
		}
		refEntity := s.reg.GetEntity(f.RefEntity)
		if refEntity == nil {
			continue
		}
		rows, err := s.store.List(ctx, refEntity.Name, refEntity)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			row["_label"] = firstStringField(row, refEntity)
		}
		opts[f.Name] = rows
	}
	return opts, nil
}

func (s *Server) render(w http.ResponseWriter, name string, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func firstStringField(row map[string]any, e *metadata.Entity) string {
	for _, f := range e.Fields {
		if f.Type == metadata.FieldTypeString {
			if v, ok := row[f.Name]; ok && v != nil {
				return fmt.Sprintf("%v", v)
			}
		}
	}
	return fmt.Sprintf("%v", row["id"])
}

func formToFields(r *http.Request, entity *metadata.Entity) map[string]any {
	fields := make(map[string]any)
	for _, f := range entity.Fields {
		val := r.FormValue(f.Name)
		if val == "" {
			fields[f.Name] = nil
			continue
		}
		switch f.Type {
		case metadata.FieldTypeDate:
			if t, err := time.Parse("2006-01-02T15:04", val); err == nil {
				fields[f.Name] = t
			} else {
				fields[f.Name] = val
			}
		case metadata.FieldTypeBool:
			fields[f.Name] = val == "true"
		default:
			fields[f.Name] = val
		}
	}
	return fields
}

func formValues(r *http.Request, entity *metadata.Entity) map[string]string {
	vals := make(map[string]string)
	for _, f := range entity.Fields {
		vals[f.Name] = r.FormValue(f.Name)
	}
	return vals
}

// resolveRefs replaces UUID values of reference fields with the display name
// of the referenced entity (first string field). Modifies rows in place.
func (s *Server) resolveRefs(ctx context.Context, entity *metadata.Entity, rows []map[string]any) {
	for _, f := range entity.Fields {
		if f.RefEntity == "" {
			continue
		}
		refEntity := s.reg.GetEntity(f.RefEntity)
		if refEntity == nil {
			continue
		}
		// collect unique IDs referenced in this field
		seen := map[string]bool{}
		for _, row := range rows {
			if v := row[f.Name]; v != nil {
				seen[fmt.Sprintf("%v", v)] = true
			}
		}
		// resolve each unique ID to a display label
		labels := make(map[string]string, len(seen))
		for idStr := range seen {
			id, err := uuid.Parse(idStr)
			if err != nil {
				continue
			}
			refRow, err := s.store.GetByID(ctx, refEntity.Name, id, refEntity)
			if err != nil {
				continue
			}
			labels[idStr] = firstStringField(refRow, refEntity)
		}
		// replace UUIDs with labels in all rows
		for _, row := range rows {
			if v := row[f.Name]; v != nil {
				if label, ok := labels[fmt.Sprintf("%v", v)]; ok {
					row[f.Name] = label
				}
			}
		}
	}
}

func listURL(entity *metadata.Entity) string {
	return fmt.Sprintf("/ui/%s/%s", strings.ToLower(string(entity.Kind)), strings.ToLower(entity.Name))
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
