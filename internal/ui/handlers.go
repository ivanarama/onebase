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
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func (s *Server) about(w http.ResponseWriter, r *http.Request) {
	entities := s.reg.Entities()
	var catalogs, docs int
	for _, e := range entities {
		if e.Kind == "catalog" {
			catalogs++
		} else {
			docs++
		}
	}
	s.render(w, "page-about", map[string]any{
		"Nav":        s.buildNav(),
		"Cfg":        s.cfg,
		"Catalogs":   catalogs,
		"Documents":  docs,
		"Registers":  len(s.reg.Registers()),
		"Reports":    len(s.reg.Reports()),
	})
}

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

	user := auth.UserFromContext(r.Context())
	isAdmin := user == nil || user.IsAdmin

	s.render(w, "page-list", map[string]any{
		"Nav":              s.buildNav(),
		"Entity":           entity,
		"Rows":             rows,
		"Params":           params,
		"RefFilterOptions": refFilterOptions,
		"IsAdmin":          isAdmin,
	})
}

func (s *Server) form(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	refOptions, _ := s.loadRefOptions(r.Context(), entity)
	tpRefOpts, _ := s.loadTPRefOptions(r.Context(), entity)
	enumOpts := s.loadEnumOptions(entity)
	// Pre-fill date fields with current datetime for new documents
	values := map[string]string{}
	if entity.Kind == metadata.KindDocument {
		now := time.Now().Format("2006-01-02T15:04")
		for _, f := range entity.Fields {
			if f.Type == metadata.FieldTypeDate {
				values[f.Name] = now
			}
		}
	}
	s.render(w, "page-form", map[string]any{
		"Nav":           s.buildNav(),
		"Entity":        entity,
		"IsNew":         true,
		"Values":        values,
		"RefOptions":    refOptions,
		"EnumOptions":   enumOpts,
		"TPRefOptions":  tpRefOpts,
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

	action := r.FormValue("_action")
	isPosting := entity.Posting && (action == "post" || action == "post_and_close")

	var dslErrMsg string
	if isPosting {
		dslErrMsg = s.runOnPostCtx(r.Context(), obj, mc)
	} else {
		dslErrMsg = s.runOnWriteCtx(r.Context(), obj, mc)
	}
	if dslErrMsg != "" {
		refOptions, _ := s.loadRefOptions(r.Context(), entity)
		tpRefOpts, _ := s.loadTPRefOptions(r.Context(), entity)
		s.render(w, "page-form", map[string]any{
			"Nav":           s.buildNav(),
			"Entity":        entity,
			"IsNew":         true,
			"Error":         dslErrMsg,
			"Values":        formValues(r, entity),
			"RefOptions":    refOptions,
			"EnumOptions":   s.loadEnumOptions(entity),
			"TPRefOptions":  tpRefOpts,
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
		if action == "post_and_close" || action == "post" {
			if err := s.saveMovements(ctx, entity.Name, obj.ID, mc); err != nil {
				return err
			}
			return s.store.SetPosted(ctx, entity.Name, obj.ID, true)
		}
		return nil
	}); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	if action == "post_and_close" {
		http.Redirect(w, r, listURL(entity), http.StatusSeeOther)
		return
	}
	// "post" / "Записать" — остаёмся на форме
	http.Redirect(w, r, "/ui/"+strings.ToLower(string(entity.Kind))+"/"+entity.Name+"/"+obj.ID.String(), http.StatusSeeOther)
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
	tpRefOpts, _ := s.loadTPRefOptions(r.Context(), entity)
	enumOpts := s.loadEnumOptions(entity)
	vals := make(map[string]string)
	for _, f := range entity.Fields {
		v := row[f.Name]
		if v == nil {
			continue
		}
		if f.Type == metadata.FieldTypeDate {
			if t, ok := v.(time.Time); ok {
				vals[f.Name] = t.Format("2006-01-02T15:04")
				continue
			}
		}
		vals[f.Name] = fmt.Sprintf("%v", v)
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

	editUser := auth.UserFromContext(r.Context())
	editIsAdmin := editUser == nil || editUser.IsAdmin

	s.render(w, "page-form", map[string]any{
		"Nav":           s.buildNav(),
		"Entity":        entity,
		"IsNew":         false,
		"Values":        vals,
		"RefOptions":    refOptions,
		"EnumOptions":   enumOpts,
		"TPRefOptions":  tpRefOpts,
		"TablePartRows": tpRows,
		"ID":            id.String(),
		"IsAdmin":       editIsAdmin,
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

	action := r.FormValue("_action")
	isPostingAct := entity.Posting && (action == "post" || action == "post_and_close")

	var dslErr2 string
	if isPostingAct {
		dslErr2 = s.runOnPostCtx(r.Context(), obj, mc)
	} else {
		dslErr2 = s.runOnWriteCtx(r.Context(), obj, mc)
	}
	if dslErr2 != "" {
		refOptions, _ := s.loadRefOptions(r.Context(), entity)
		tpRefOpts2, _ := s.loadTPRefOptions(r.Context(), entity)
		s.render(w, "page-form", map[string]any{
			"Nav":           s.buildNav(),
			"Entity":        entity,
			"IsNew":         false,
			"Error":         dslErr2,
			"Values":        formValues(r, entity),
			"RefOptions":    refOptions,
			"EnumOptions":   s.loadEnumOptions(entity),
			"TPRefOptions":  tpRefOpts2,
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
		if action == "post_and_close" || action == "post" {
			if err := s.saveMovements(ctx, entity.Name, obj.ID, mc); err != nil {
				return err
			}
			return s.store.SetPosted(ctx, entity.Name, obj.ID, true)
		}
		// "Записать" для проводимого документа: сбрасываем проведение
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

	if action == "post_and_close" {
		http.Redirect(w, r, listURL(entity), http.StatusSeeOther)
		return
	}
	// "Записать" — остаёмся на форме
	http.Redirect(w, r, "/ui/"+strings.ToLower(string(entity.Kind))+"/"+entity.Name+"/"+id.String(), http.StatusSeeOther)
}

// postDocument posts a document: runs ОбработкаПроведения, writes movements, sets posted=true.
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

	if errMsg := s.runOnPostCtx(r.Context(), obj, mc); errMsg != "" {
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

// deleteRecord: admin → permanent delete (with ref check); non-admin → mark for deletion.
func (s *Server) deleteRecord(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}

	user := auth.UserFromContext(r.Context())
	isAdmin := user == nil || user.IsAdmin // no auth configured → treat as admin
	markOnly := r.URL.Query().Get("mark") == "1"

	if !isAdmin || markOnly {
		// Non-admin or explicit mark-only: mark for deletion
		if err := s.store.MarkForDeletion(r.Context(), entity.Name, id, true); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		http.Redirect(w, r, listURL(entity), http.StatusSeeOther)
		return
	}

	// Admin: check references before permanent delete
	refs := s.store.CheckRefs(r.Context(), entity.Name, id, s.reg.Entities())
	if len(refs) > 0 {
		var msg strings.Builder
		msg.WriteString("Невозможно удалить: объект используется в:\n")
		for _, ref := range refs {
			fmt.Fprintf(&msg, "  • %s.%s (%d записей)\n", ref.EntityName, ref.FieldName, ref.Count)
		}
		http.Error(w, msg.String(), 409)
		return
	}

	if err := s.store.WithTx(r.Context(), func(ctx context.Context) error {
		if entity.Posting {
			for _, reg := range s.reg.Registers() {
				if err := s.store.WriteMovements(ctx, reg.Name, entity.Name, id, nil, reg, nil); err != nil {
					return err
				}
			}
		}
		return s.store.Delete(ctx, entity.Name, id)
	}); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, listURL(entity), http.StatusSeeOther)
}

// deleteMarkedAll is the global "Удалить помеченные" page accessible from the system menu.
// GET: shows all marked records across every entity.
// POST: deletes all marked records that have no references.
func (s *Server) deleteMarkedAll(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		http.Error(w, "доступ запрещён", http.StatusForbidden)
		return
	}

	type markedEntry struct {
		EntityName string
		Kind       string
		ID         string
		Label      string
		HasRefs    bool
	}

	if r.Method == http.MethodPost {
		deleted, skipped := 0, 0
		for _, entity := range s.reg.Entities() {
			marked, err := s.store.ListMarked(r.Context(), entity.Name, entity)
			if err != nil {
				continue
			}
			for _, row := range marked {
				idStr, _ := row["id"].(string)
				id, err := uuid.Parse(idStr)
				if err != nil {
					continue
				}
				refs := s.store.CheckRefs(r.Context(), entity.Name, id, s.reg.Entities())
				if len(refs) > 0 {
					skipped++
					continue
				}
				s.store.WithTx(r.Context(), func(ctx context.Context) error {
					if entity.Posting {
						for _, reg := range s.reg.Registers() {
							s.store.WriteMovements(ctx, reg.Name, entity.Name, id, nil, reg, nil)
						}
					}
					return s.store.Delete(ctx, entity.Name, id)
				})
				deleted++
			}
		}
		http.Redirect(w, r,
			fmt.Sprintf("/ui/delete-marked?deleted=%d&skipped=%d", deleted, skipped),
			http.StatusSeeOther)
		return
	}

	// GET: collect all marked records
	var entries []markedEntry
	for _, entity := range s.reg.Entities() {
		rows, err := s.store.ListMarked(r.Context(), entity.Name, entity)
		if err != nil {
			continue
		}
		for _, row := range rows {
			idStr, _ := row["id"].(string)
			id, _ := uuid.Parse(idStr)
			refs := s.store.CheckRefs(r.Context(), entity.Name, id, s.reg.Entities())
			entries = append(entries, markedEntry{
				EntityName: entity.Name,
				Kind:       string(entity.Kind),
				ID:         idStr,
				Label:      firstStringField(row, entity),
				HasRefs:    len(refs) > 0,
			})
		}
	}

	deleted, _ := strconv.Atoi(r.URL.Query().Get("deleted"))
	skipped, _ := strconv.Atoi(r.URL.Query().Get("skipped"))
	s.render(w, "page-delete-marked", map[string]any{
		"Nav":     s.buildNav(),
		"Entries": entries,
		"Deleted": deleted,
		"Skipped": skipped,
	})
}

// deleteMarked permanently deletes all deletion_mark=true records without references.
func (s *Server) deleteMarked(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}

	user := auth.UserFromContext(r.Context())
	if user != nil && !user.IsAdmin {
		http.Error(w, "доступ запрещён", 403)
		return
	}

	marked, err := s.store.ListMarked(r.Context(), entity.Name, entity)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	deleted, skipped := 0, 0
	for _, row := range marked {
		idStr, _ := row["id"].(string)
		id, err := uuid.Parse(idStr)
		if err != nil {
			continue
		}
		refs := s.store.CheckRefs(r.Context(), entity.Name, id, s.reg.Entities())
		if len(refs) > 0 {
			skipped++
			continue
		}
		s.store.WithTx(r.Context(), func(ctx context.Context) error {
			if entity.Posting {
				for _, reg := range s.reg.Registers() {
					s.store.WriteMovements(ctx, reg.Name, entity.Name, id, nil, reg, nil)
				}
			}
			return s.store.Delete(ctx, entity.Name, id)
		})
		deleted++
	}

	http.Redirect(w, r,
		fmt.Sprintf("%s?deleted=%d&skipped=%d", listURL(entity), deleted, skipped),
		http.StatusSeeOther)
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
	s.resolveRegisterRows(r.Context(), rows, reg)
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
	s.resolveRegisterRows(r.Context(), rows, reg)
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
	// Build query params: convert date strings to time.Time for proper PG type inference.
	// Keep paramValues unchanged so the form repopulates with the original strings.
	queryValues := make(map[string]any, len(paramValues))
	for k, v := range paramValues {
		queryValues[k] = v
	}
	for _, p := range rep.Params {
		if p.Type == "date" {
			if str, ok := queryValues[p.Name].(string); ok && str != "" {
				if t, err2 := time.Parse("2006-01-02", str); err2 == nil {
					queryValues[p.Name] = t
				}
			}
		}
	}
	compiled, err := query.Compile(rep.Query, queryValues)
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
	s.resolveUUIDsInReport(r.Context(), rows)
	s.render(w, "page-report", map[string]any{
		"Nav":         s.buildNav(),
		"Report":      rep,
		"Cols":        cols,
		"Rows":        rows,
		"ParamValues": paramValues,
	})
}

// resolveUUIDsInReport replaces UUID-looking strings in report rows with entity display names.
func (s *Server) resolveUUIDsInReport(ctx context.Context, rows []map[string]any) {
	uuidToLabel := make(map[string]string)
	for _, row := range rows {
		for _, v := range row {
			if str, ok := v.(string); ok {
				if _, err := uuid.Parse(str); err == nil {
					uuidToLabel[str] = ""
				}
			}
		}
	}
	if len(uuidToLabel) == 0 {
		return
	}
	for _, entity := range s.reg.Entities() {
		for idStr, label := range uuidToLabel {
			if label != "" {
				continue
			}
			id, _ := uuid.Parse(idStr)
			if refRow, err := s.store.GetByID(ctx, entity.Name, id, entity); err == nil {
				uuidToLabel[idStr] = firstStringField(refRow, entity)
			}
		}
	}
	for _, row := range rows {
		for col, v := range row {
			if str, ok := v.(string); ok {
				if label, found := uuidToLabel[str]; found && label != "" {
					row[col] = label
				}
			}
		}
	}
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
	return s.runOnWriteCtx(context.Background(), obj, mc)
}

func (s *Server) buildDSLVars(ctx context.Context, mc *runtime.MovementsCollector) map[string]any {
	enumsMap := make(map[string]any)
	for _, e := range s.reg.Enums() {
		inner := make(map[string]any, len(e.Values))
		for _, v := range e.Values {
			inner[v] = v
		}
		enumsMap[e.Name] = &interpreter.MapThis{M: inner}
	}
	constsMap := make(map[string]any)
	if vals, err := s.store.ListConstants(ctx); err == nil {
		constsMap = vals
	}
	return map[string]any{
		"Движения":     mc,
		"Перечисления": &interpreter.MapThis{M: enumsMap},
		"Константы":    &interpreter.MapThis{M: constsMap},
	}
}

func (s *Server) runOnWriteCtx(ctx context.Context, obj *runtime.Object, mc *runtime.MovementsCollector) string {
	proc := s.reg.GetProcedure(obj.Type, "OnWrite")
	if proc == nil {
		return ""
	}
	if err := s.interp.Run(proc, obj, s.buildDSLVars(ctx, mc)); err != nil {
		if dslErr, ok := err.(*interpreter.DSLError); ok {
			return dslErr.Msg
		}
		return err.Error()
	}
	return ""
}

func (s *Server) runOnPostCtx(ctx context.Context, obj *runtime.Object, mc *runtime.MovementsCollector) string {
	proc := s.reg.GetProcedure(obj.Type, "OnPost")
	if proc == nil {
		return ""
	}
	if err := s.interp.Run(proc, obj, s.buildDSLVars(ctx, mc)); err != nil {
		if dslErr, ok := err.(*interpreter.DSLError); ok {
			return dslErr.Msg
		}
		return err.Error()
	}
	return ""
}

func (s *Server) getEntity(w http.ResponseWriter, r *http.Request) *metadata.Entity {
	raw := chi.URLParam(r, "entity")
	// chi may return the raw percent-encoded path segment — decode it
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		decoded = raw
	}
	if e := s.reg.GetEntityBySlug(decoded); e != nil {
		return e
	}
	http.Error(w, "unknown entity: "+raw, 404)
	return nil
}

// loadEnumOptions returns enum values for each enum-type field of the entity.
func (s *Server) loadEnumOptions(entity *metadata.Entity) map[string][]string {
	opts := make(map[string][]string)
	for _, f := range entity.Fields {
		if f.EnumName == "" {
			continue
		}
		en := s.reg.GetEnum(f.EnumName)
		if en == nil {
			continue
		}
		opts[f.Name] = en.Values
	}
	return opts
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

// loadTPRefOptions returns select options for reference fields in all table parts.
// Result: tpName → fieldName → [{id, _label, ...}]
func (s *Server) loadTPRefOptions(ctx context.Context, entity *metadata.Entity) (map[string]map[string][]map[string]any, error) {
	result := make(map[string]map[string][]map[string]any)
	for _, tp := range entity.TableParts {
		tpOpts := make(map[string][]map[string]any)
		for _, f := range tp.Fields {
			if f.RefEntity == "" {
				continue
			}
			// Always mark the field as a reference (even if catalog empty or missing)
			tpOpts[f.Name] = []map[string]any{}
			refEntity := s.reg.GetEntity(f.RefEntity)
			if refEntity == nil {
				continue
			}
			rows, err := s.store.List(ctx, refEntity.Name, refEntity, storage.ListParams{})
			if err != nil {
				continue
			}
			for _, row := range rows {
				row["_label"] = firstStringField(row, refEntity)
			}
			tpOpts[f.Name] = rows
		}
		// Always add TP entry so JS knows which fields are references
		result[tp.Name] = tpOpts
	}
	return result, nil
}

// resolveRegisterRows enriches register movement rows with human-readable values:
// recorder_label = "TypeName №Num от Date", dimension UUID values → catalog names.
func (s *Server) resolveRegisterRows(ctx context.Context, rows []map[string]any, reg *metadata.Register) {
	// collect all UUID-looking strings in dimension fields
	uuidToLabel := make(map[string]string)
	for _, row := range rows {
		for _, f := range reg.Dimensions {
			if v, ok := row[f.Name].(string); ok {
				if _, err := uuid.Parse(v); err == nil {
					uuidToLabel[v] = "" // mark for lookup
				}
			}
		}
	}
	// resolve UUIDs by scanning all entities
	if len(uuidToLabel) > 0 {
		for _, entity := range s.reg.Entities() {
			for idStr, label := range uuidToLabel {
				if label != "" {
					continue // already resolved
				}
				id, _ := uuid.Parse(idStr)
				refRow, err := s.store.GetByID(ctx, entity.Name, id, entity)
				if err == nil {
					uuidToLabel[idStr] = firstStringField(refRow, entity)
				}
			}
		}
	}
	// enrich each row
	for _, row := range rows {
		// recorder label
		recType, _ := row["recorder_type"].(string)
		recIDStr, _ := row["recorder"].(string)
		if recType != "" && recIDStr != "" {
			if recID, err := uuid.Parse(recIDStr); err == nil {
				if entity := s.reg.GetEntityBySlug(recType); entity != nil {
					if docRow, err2 := s.store.GetByID(ctx, entity.Name, recID, entity); err2 == nil {
						num := fmt.Sprintf("%v", docRow["Номер"])
						date := regFmtDate(docRow["Дата"])
						row["recorder_label"] = fmt.Sprintf("%s №%s от %s", entity.Name, num, date)
					}
				}
			}
		}
		// dimension UUID → name
		for _, f := range reg.Dimensions {
			if v, ok := row[f.Name].(string); ok {
				if label, found := uuidToLabel[v]; found && label != "" {
					row[f.Name] = label
				}
			}
		}
	}
}

func regFmtDate(v any) string {
	if t, ok := v.(time.Time); ok {
		return t.Format("02.01.2006")
	}
	if s, ok := v.(string); ok {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.Format("02.01.2006")
		}
	}
	return fmt.Sprintf("%v", v)
}

func (s *Server) render(w http.ResponseWriter, name string, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Always inject Cfg so every template can access app name / version
	if _, ok := data["Cfg"]; !ok {
		data["Cfg"] = s.cfg
	}
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
			parsed := false
			for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
				if t, err := time.Parse(layout, val); err == nil {
					fields[f.Name] = t
					parsed = true
					break
				}
			}
			if !parsed {
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

func (s *Server) getInfoReg(w http.ResponseWriter, r *http.Request) *metadata.InfoRegister {
	name := capitalize(chi.URLParam(r, "name"))
	ir := s.reg.GetInfoRegister(name)
	if ir == nil {
		http.Error(w, "unknown info register: "+name, 404)
	}
	return ir
}

func (s *Server) infoRegList(w http.ResponseWriter, r *http.Request) {
	ir := s.getInfoReg(w, r)
	if ir == nil {
		return
	}
	rows, err := s.store.InfoRegList(r.Context(), ir)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "page-inforeg-list", map[string]any{
		"Nav":      s.buildNav(),
		"InfoReg":  ir,
		"Rows":     rows,
	})
}

func (s *Server) infoRegForm(w http.ResponseWriter, r *http.Request) {
	ir := s.getInfoReg(w, r)
	if ir == nil {
		return
	}
	now := time.Now().Format("2006-01-02")
	s.render(w, "page-inforeg-form", map[string]any{
		"Nav":     s.buildNav(),
		"InfoReg": ir,
		"Values":  map[string]string{"period": now},
		"Error":   "",
	})
}

func (s *Server) infoRegSubmit(w http.ResponseWriter, r *http.Request) {
	ir := s.getInfoReg(w, r)
	if ir == nil {
		return
	}
	r.ParseForm()

	var periodPtr *time.Time
	if ir.Periodic {
		pStr := r.FormValue("period")
		if pStr == "" {
			s.render(w, "page-inforeg-form", map[string]any{
				"Nav": s.buildNav(), "InfoReg": ir,
				"Values": formValuesFromRequest(r, ir),
				"Error":  "Период обязателен для периодического регистра",
			})
			return
		}
		for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
			if t, err := time.Parse(layout, pStr); err == nil {
				periodPtr = &t
				break
			}
		}
		if periodPtr == nil {
			s.render(w, "page-inforeg-form", map[string]any{
				"Nav": s.buildNav(), "InfoReg": ir,
				"Values": formValuesFromRequest(r, ir),
				"Error":  "Неверный формат даты периода",
			})
			return
		}
	}

	dims := parseInfoRegFields(r, ir.Dimensions)
	resources := parseInfoRegFields(r, ir.Resources)

	if err := s.store.InfoRegSet(r.Context(), ir, dims, resources, periodPtr); err != nil {
		s.render(w, "page-inforeg-form", map[string]any{
			"Nav": s.buildNav(), "InfoReg": ir,
			"Values": formValuesFromRequest(r, ir),
			"Error":  err.Error(),
		})
		return
	}
	http.Redirect(w, r, "/ui/inforeg/"+strings.ToLower(ir.Name), http.StatusFound)
}

func (s *Server) infoRegDelete(w http.ResponseWriter, r *http.Request) {
	ir := s.getInfoReg(w, r)
	if ir == nil {
		return
	}
	r.ParseForm()

	var periodPtr *time.Time
	if ir.Periodic {
		if pStr := r.FormValue("period"); pStr != "" {
			for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
				if t, err := time.Parse(layout, pStr); err == nil {
					periodPtr = &t
					break
				}
			}
		}
	}
	dims := parseInfoRegFields(r, ir.Dimensions)
	if err := s.store.InfoRegDelete(r.Context(), ir, dims, periodPtr); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/ui/inforeg/"+strings.ToLower(ir.Name), http.StatusFound)
}

func parseInfoRegFields(r *http.Request, fields []metadata.Field) map[string]any {
	result := make(map[string]any, len(fields))
	for _, f := range fields {
		val := r.FormValue(f.Name)
		if val == "" {
			result[f.Name] = nil
			continue
		}
		result[f.Name] = parseInfoRegFieldValue(f, val)
	}
	return result
}

func parseInfoRegFieldValue(f metadata.Field, val string) any {
	switch f.Type {
	case metadata.FieldTypeDate:
		for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
			if t, err := time.Parse(layout, val); err == nil {
				return t
			}
		}
		return val
	case metadata.FieldTypeBool:
		return val == "true" || val == "on"
	default:
		return val
	}
}

func (s *Server) constantsList(w http.ResponseWriter, r *http.Request) {
	consts := s.reg.Constants()
	sort.Slice(consts, func(i, j int) bool { return consts[i].Name < consts[j].Name })

	values, _ := s.store.ListConstants(r.Context())
	valStrs := make(map[string]string, len(values))
	for k, v := range values {
		valStrs[k] = fmt.Sprintf("%v", v)
	}

	// ref options for reference-type constants
	refOpts := make(map[string][]map[string]any)
	for _, c := range consts {
		if c.RefEntity == "" {
			continue
		}
		refEntity := s.reg.GetEntity(c.RefEntity)
		if refEntity == nil {
			continue
		}
		rows, err := s.store.List(r.Context(), refEntity.Name, refEntity, storage.ListParams{})
		if err != nil {
			continue
		}
		for _, row := range rows {
			row["_label"] = firstStringField(row, refEntity)
		}
		refOpts[c.Name] = rows
	}

	msg := r.URL.Query().Get("saved")
	s.render(w, "page-constants", map[string]any{
		"Nav":       s.buildNav(),
		"Constants": consts,
		"Values":    valStrs,
		"RefOpts":   refOpts,
		"Saved":     msg == "1",
	})
}

func (s *Server) constantsSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	consts := s.reg.Constants()
	for _, c := range consts {
		val := r.FormValue(c.Name)
		var v any
		if val == "" {
			v = nil
		} else {
			v = val
		}
		if err := s.store.SetConstant(r.Context(), c.Name, v); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	http.Redirect(w, r, "/ui/constants?saved=1", http.StatusSeeOther)
}

func formValuesFromRequest(r *http.Request, ir *metadata.InfoRegister) map[string]string {
	vals := map[string]string{"period": r.FormValue("period")}
	for _, f := range ir.Dimensions {
		vals[f.Name] = r.FormValue(f.Name)
	}
	for _, f := range ir.Resources {
		vals[f.Name] = r.FormValue(f.Name)
	}
	return vals
}
