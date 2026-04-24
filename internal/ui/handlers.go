package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	s.render(w, "page-index", map[string]any{"Nav": s.buildNav()})
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	params := parseListParams(r, entity)
	rows, err := s.store.List(r.Context(), entity.Name, entity, params)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.resolveRefs(r.Context(), entity, rows)

	refFilterOptions, _ := s.loadRefOptions(r.Context(), entity)

	s.render(w, "page-list", map[string]any{
		"Nav":              s.buildNav(),
		"Entity":           entity,
		"Rows":             rows,
		"Params":           params,
		"RefFilterOptions": refFilterOptions,
	})
}

func (s *Server) form(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	refOptions, _ := s.loadRefOptions(r.Context(), entity)
	s.render(w, "page-form", map[string]any{
		"Nav":           s.buildNav(),
		"Entity":        entity,
		"IsNew":         true,
		"Values":        map[string]string{},
		"RefOptions":    refOptions,
		"TablePartRows": map[string][]map[string]any{},
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
	tpRows := parseTablePartRows(r, entity)

	obj := runtime.NewObject(entity.Name, entity.Kind)
	for k, v := range fields {
		obj.Set(k, v)
	}
	obj.TablePartRows = tpRows

	// Auto-number: fill Номер if empty for new documents
	if entity.Kind == metadata.KindDocument {
		for _, f := range entity.Fields {
			if f.Name == "Номер" && f.Type == metadata.FieldTypeString {
				if v := fmt.Sprintf("%v", obj.Fields["Номер"]); v == "" || v == "<nil>" {
					if n, err := s.store.NextNum(r.Context(), entity.Name); err == nil {
						obj.Set("Номер", fmt.Sprintf("%06d", n))
					}
				}
				break
			}
		}
	}

	mc := runtime.NewMovementsCollector(entity.Name, obj.ID)
	setPeriodFromFields(mc, entity, obj.Fields)

	if errMsg := s.runOnWrite(obj, mc); errMsg != "" {
		refOptions, _ := s.loadRefOptions(r.Context(), entity)
		s.render(w, "page-form", map[string]any{
			"Nav":           s.buildNav(),
			"Entity":        entity,
			"IsNew":         true,
			"Error":         errMsg,
			"Values":        formValues(r, entity),
			"RefOptions":    refOptions,
			"TablePartRows": tpRows,
		})
		return
	}

	if err := s.store.WithTx(r.Context(), func(ctx context.Context) error {
		if err := s.store.Upsert(ctx, entity.Name, obj.ID, obj.Fields, entity); err != nil {
			return err
		}
		if err := s.saveTablePartsDirect(ctx, entity, obj.ID, obj.TablePartRows); err != nil {
			return err
		}
		// For posting documents, movements are written only on explicit Post action
		if !entity.Posting {
			return s.saveMovements(ctx, entity.Name, obj.ID, mc)
		}
		return nil
	}); err != nil {
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
	// Include posted status for documents
	if entity.Kind == metadata.KindDocument {
		vals["posted"] = fmt.Sprintf("%v", row["posted"])
	}

	tpRows := make(map[string][]map[string]any)
	for _, tp := range entity.TableParts {
		rows, err := s.store.GetTablePartRows(r.Context(), entity.Name, tp.Name, id, tp)
		if err == nil {
			tpRows[tp.Name] = rows
		}
	}

	s.render(w, "page-form", map[string]any{
		"Nav":           s.buildNav(),
		"Entity":        entity,
		"IsNew":         false,
		"Values":        vals,
		"RefOptions":    refOptions,
		"TablePartRows": tpRows,
		"ID":            id.String(),
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
	tpRows := parseTablePartRows(r, entity)

	obj := &runtime.Object{
		Type:          entity.Name,
		Kind:          entity.Kind,
		ID:            id,
		Fields:        fields,
		TablePartRows: tpRows,
	}
	mc := runtime.NewMovementsCollector(entity.Name, id)
	setPeriodFromFields(mc, entity, fields)

	if errMsg := s.runOnWrite(obj, mc); errMsg != "" {
		refOptions, _ := s.loadRefOptions(r.Context(), entity)
		s.render(w, "page-form", map[string]any{
			"Nav":           s.buildNav(),
			"Entity":        entity,
			"IsNew":         false,
			"Error":         errMsg,
			"Values":        formValues(r, entity),
			"RefOptions":    refOptions,
			"TablePartRows": tpRows,
		})
		return
	}

	if err := s.store.WithTx(r.Context(), func(ctx context.Context) error {
		if err := s.store.Upsert(ctx, entity.Name, obj.ID, obj.Fields, entity); err != nil {
			return err
		}
		if err := s.saveTablePartsDirect(ctx, entity, obj.ID, obj.TablePartRows); err != nil {
			return err
		}
		if !entity.Posting {
			return s.saveMovements(ctx, entity.Name, obj.ID, mc)
		}
		// Posting document: clear movements on edit (must re-post explicitly)
		for _, reg := range s.reg.Registers() {
			if err := s.store.WriteMovements(ctx, reg.Name, entity.Name, obj.ID, nil, reg, nil); err != nil {
				return err
			}
		}
		return s.store.SetPosted(ctx, entity.Name, obj.ID, false)
	}); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	http.Redirect(w, r, listURL(entity), http.StatusSeeOther)
}

// postDocument posts a document: runs OnWrite, writes movements, sets posted=true.
func (s *Server) postDocument(w http.ResponseWriter, r *http.Request) {
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

	obj := &runtime.Object{ID: id, Type: entity.Name, Kind: entity.Kind, Fields: make(map[string]any)}
	for _, f := range entity.Fields {
		obj.Fields[f.Name] = row[f.Name]
	}
	tpRows := make(map[string][]map[string]any)
	for _, tp := range entity.TableParts {
		rows, _ := s.store.GetTablePartRows(r.Context(), entity.Name, tp.Name, id, tp)
		tpRows[tp.Name] = rows
	}
	obj.TablePartRows = tpRows

	mc := runtime.NewMovementsCollector(entity.Name, id)
	setPeriodFromFields(mc, entity, obj.Fields)

	if errMsg := s.runOnWrite(obj, mc); errMsg != "" {
		http.Error(w, "Проведение: "+errMsg, 422)
		return
	}

	if err := s.store.WithTx(r.Context(), func(ctx context.Context) error {
		if err := s.saveMovements(ctx, entity.Name, id, mc); err != nil {
			return err
		}
		return s.store.SetPosted(ctx, entity.Name, id, true)
	}); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, listURL(entity), http.StatusSeeOther)
}

// unpostDocument clears movements and sets posted=false.
func (s *Server) unpostDocument(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}

	if err := s.store.WithTx(r.Context(), func(ctx context.Context) error {
		for _, reg := range s.reg.Registers() {
			if err := s.store.WriteMovements(ctx, reg.Name, entity.Name, id, nil, reg, nil); err != nil {
				return err
			}
		}
		return s.store.SetPosted(ctx, entity.Name, id, false)
	}); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, listURL(entity), http.StatusSeeOther)
}

func (s *Server) saveMovements(ctx context.Context, docType string, docID uuid.UUID, mc *runtime.MovementsCollector) error {
	for regName, rows := range mc.All() {
		reg := s.reg.GetRegister(regName)
		if reg == nil {
			continue
		}
		if err := s.store.WriteMovements(ctx, regName, docType, docID, rows, reg, mc.Period); err != nil {
			return err
		}
	}
	return nil
}

// setPeriodFromFields sets the movements period from the first date field of the document.
func setPeriodFromFields(mc *runtime.MovementsCollector, entity *metadata.Entity, fields map[string]any) {
	for _, f := range entity.Fields {
		if f.Type == metadata.FieldTypeDate {
			if v, ok := fields[f.Name]; ok && v != nil {
				if t, ok := v.(time.Time); ok {
					mc.SetPeriod(t)
				}
			}
			return
		}
	}
}

func (s *Server) registerMovements(w http.ResponseWriter, r *http.Request) {
	name := capitalize(chi.URLParam(r, "name"))
	reg := s.reg.GetRegister(name)
	if reg == nil {
		http.Error(w, "unknown register: "+name, 404)
		return
	}
	rows, err := s.store.GetMovements(r.Context(), name, reg)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "page-register-movements", map[string]any{
		"Nav":      s.buildNav(),
		"Register": reg,
		"Rows":     rows,
	})
}

func (s *Server) registerBalances(w http.ResponseWriter, r *http.Request) {
	name := capitalize(chi.URLParam(r, "name"))
	reg := s.reg.GetRegister(name)
	if reg == nil {
		http.Error(w, "unknown register: "+name, 404)
		return
	}
	rows, err := s.store.GetBalances(r.Context(), name, reg)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "page-register-balances", map[string]any{
		"Nav":      s.buildNav(),
		"Register": reg,
		"Rows":     rows,
	})
}

func (s *Server) reportForm(w http.ResponseWriter, r *http.Request) {
	rep := s.getReport(w, r)
	if rep == nil {
		return
	}
	// If report has no params, run immediately.
	if len(rep.Params) == 0 {
		s.runReport(w, r, rep, map[string]any{})
		return
	}
	s.render(w, "page-report", map[string]any{
		"Nav":         s.buildNav(),
		"Report":      rep,
		"ParamValues": map[string]any{},
	})
}

func (s *Server) reportRun(w http.ResponseWriter, r *http.Request) {
	rep := s.getReport(w, r)
	if rep == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	paramValues := make(map[string]any, len(rep.Params))
	for _, p := range rep.Params {
		val := r.FormValue(p.Name)
		if val == "" {
			paramValues[p.Name] = nil
		} else {
			paramValues[p.Name] = val
		}
	}
	s.runReport(w, r, rep, paramValues)
}

func (s *Server) getReport(w http.ResponseWriter, r *http.Request) *reportpkg.Report {
	name := chi.URLParam(r, "name")
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	rep := s.reg.GetReport(name)
	if rep == nil {
		http.Error(w, "unknown report: "+name, 404)
		return nil
	}
	return rep
}

func (s *Server) runReport(w http.ResponseWriter, r *http.Request, rep *reportpkg.Report, paramValues map[string]any) {
	compiled, err := query.Compile(rep.Query, paramValues)
	if err != nil {
		s.render(w, "page-report", map[string]any{
			"Nav":         s.buildNav(),
			"Report":      rep,
			"QueryError":  err.Error(),
			"ParamValues": paramValues,
		})
		return
	}
	rows, cols, err := s.store.RunQuery(r.Context(), compiled.SQL, compiled.Args)
	if err != nil {
		s.render(w, "page-report", map[string]any{
			"Nav":         s.buildNav(),
			"Report":      rep,
			"QueryError":  err.Error(),
			"ParamValues": paramValues,
		})
		return
	}
	s.render(w, "page-report", map[string]any{
		"Nav":         s.buildNav(),
		"Report":      rep,
		"Cols":        cols,
		"Rows":        rows,
		"ParamValues": paramValues,
	})
}

// saveTablePartsDirect persists tablepart rows from the provided map (possibly modified by DSL).
func (s *Server) saveTablePartsDirect(ctx context.Context, entity *metadata.Entity, parentID uuid.UUID, tpRows map[string][]map[string]any) error {
	for _, tp := range entity.TableParts {
		rows := tpRows[tp.Name]
		if rows == nil {
			rows = []map[string]any{}
		}
		if err := s.store.UpsertTablePartRows(ctx, entity.Name, tp.Name, parentID, rows, tp); err != nil {
			return err
		}
	}
	return nil
}

// parseTablePartRows reads tp.{TpName}.{idx}.{FieldName} form values.
func parseTablePartRows(r *http.Request, entity *metadata.Entity) map[string][]map[string]any {
	result := make(map[string][]map[string]any)
	for _, tp := range entity.TableParts {
		// collect max index
		maxIdx := -1
		prefix := "tp." + tp.Name + "."
		for key := range r.Form {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			rest := strings.TrimPrefix(key, prefix)
			parts := strings.SplitN(rest, ".", 2)
			if len(parts) < 2 {
				continue
			}
			if idx, err := strconv.Atoi(parts[0]); err == nil && idx > maxIdx {
				maxIdx = idx
			}
		}
		if maxIdx < 0 {
			result[tp.Name] = []map[string]any{}
			continue
		}
		rows := make([]map[string]any, maxIdx+1)
		for i := range rows {
			rows[i] = make(map[string]any)
		}
		for key, vals := range r.Form {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			rest := strings.TrimPrefix(key, prefix)
			parts := strings.SplitN(rest, ".", 2)
			if len(parts) < 2 {
				continue
			}
			idx, err := strconv.Atoi(parts[0])
			if err != nil {
				continue
			}
			fieldName := parts[1]
			if len(vals) > 0 {
				rows[idx][fieldName] = vals[0]
			}
		}
		// filter empty rows (all fields blank) and convert types
		var cleaned []map[string]any
		for _, row := range rows {
			empty := true
			for _, f := range tp.Fields {
				if v, ok := row[f.Name]; ok && fmt.Sprintf("%v", v) != "" {
					empty = false
					break
				}
			}
			if !empty {
				converted := make(map[string]any, len(row))
				for _, f := range tp.Fields {
					raw := fmt.Sprintf("%v", row[f.Name])
					switch f.Type {
					case metadata.FieldTypeNumber:
						converted[f.Name] = raw
					case metadata.FieldTypeBool:
						converted[f.Name] = raw == "true"
					default:
						converted[f.Name] = raw
					}
				}
				cleaned = append(cleaned, converted)
			}
		}
		result[tp.Name] = cleaned
	}
	return result
}

// parseListParams reads filter and sort URL params.
func parseListParams(r *http.Request, entity *metadata.Entity) storage.ListParams {
	q := r.URL.Query()
	params := storage.ListParams{
		Filters: make(map[string]storage.FilterValue),
		Sort:    q.Get("sort"),
		Dir:     q.Get("dir"),
	}
	for _, f := range entity.Fields {
		switch f.Type {
		case metadata.FieldTypeDate:
			from := q.Get("f." + f.Name + ".from")
			to := q.Get("f." + f.Name + ".to")
			if from != "" || to != "" {
				params.Filters[f.Name] = storage.FilterValue{From: from, To: to}
			}
		default:
			val := q.Get("f." + f.Name)
			if val != "" {
				params.Filters[f.Name] = storage.FilterValue{Value: val}
			}
		}
	}
	return params
}

func (s *Server) runOnWrite(obj *runtime.Object, mc *runtime.MovementsCollector) string {
	proc := s.reg.GetProcedure(obj.Type, "OnWrite")
	if proc == nil {
		return ""
	}
	vars := map[string]any{"Движения": mc}
	if err := s.interp.Run(proc, obj, vars); err != nil {
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
		rows, err := s.store.List(ctx, refEntity.Name, refEntity, storage.ListParams{})
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

// sortKeys returns map keys in sorted order (for deterministic template output).
func sortKeys(m map[string]storage.FilterValue) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// filterValue returns the FilterValue for a field from ListParams, or empty.
func filterValue(params storage.ListParams, fieldName string) storage.FilterValue {
	if params.Filters == nil {
		return storage.FilterValue{}
	}
	return params.Filters[fieldName]
}
