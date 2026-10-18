package ui

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
)

type entityListRow struct {
	Cells  []string `json:"cells"`
	URL    string   `json:"url,omitempty"`
	Marked bool     `json:"marked,omitempty"`
}
type entityListData struct {
	Status  string          `json:"status"`
	Columns []string        `json:"columns"`
	Rows    []entityListRow `json:"rows"`
}
type entityListOwnerIDKey struct{}

type entityListState struct {
	Owner, Form, ID, User, Schema string
	Expires                       int64
	Values                        map[string]string
}

func (s *Server) entityListStateMAC(body []byte) []byte {
	s.entityListKeyOnce.Do(func() {
		if _, err := rand.Read(s.entityListKey[:]); err != nil {
			panic(err)
		}
	})
	m := hmac.New(sha256.New, s.entityListKey[:])
	_, _ = m.Write(body)
	return m.Sum(nil)
}
func entityListUser(ctx context.Context) string {
	u := auth.UserFromContext(ctx)
	if u == nil {
		return ""
	}
	return u.Login
}
func entityListSchema(form *metadata.FormModule) (string, error) {
	b, err := json.Marshal(form.Elements)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}
func listValueString(v any) string {
	if v == nil {
		return ""
	}
	if t, ok := v.(time.Time); ok {
		if t.IsZero() {
			return ""
		}
		return t.Format(time.RFC3339)
	}
	return fmt.Sprint(v)
}
func listValue(values map[string]any, name string) any {
	for k, v := range values {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return nil
}
func setListValue(values map[string]any, name string, value any) {
	for key := range values {
		if strings.EqualFold(key, name) {
			delete(values, key)
		}
	}
	values[name] = value
}
func listSourceString(owner *metadata.Entity, form *metadata.FormModule, path string, value any) string {
	field := metadata.EntityListSourceField(owner, form, path)
	if field != nil && field.RefEntity != "" {
		if _, id, ok := uuidFromValue(value); ok {
			return id.String()
		}
	}
	return listValueString(value)
}
func hasManagedEntityLists(form *metadata.FormModule) bool {
	if form == nil {
		return false
	}
	found := false
	form.Walk(func(el *metadata.FormElement) bool {
		if el.Kind == metadata.FormElementEntityList {
			found = true
		}
		return !found
	})
	return found
}
func listSources(form *metadata.FormModule) map[string]string {
	out := map[string]string{}
	if form == nil {
		return out
	}
	form.Walk(func(el *metadata.FormElement) bool {
		if el.Kind == metadata.FormElementEntityList && el.EntityList != nil {
			for _, c := range el.EntityList.Filter {
				if c.Source != "" {
					out[c.Source] = dpFieldName(c.Source)
				}
			}
		}
		return true
	})
	return out
}
func (s *Server) listSourceWritable(owner *metadata.Entity, form *metadata.FormModule, path string, values map[string]any, admin bool) bool {
	writable := false
	states := s.formElementStates(form, owner, values, admin)
	evaluationFailed := false
	if s.interp != nil {
		_, _, _, warnings := managedFormElementStates(form, managedFormHeaderValues(owner, values), newInterpEvaluator(s.interp))
		evaluationFailed = len(warnings) > 0
	}
	walkBrowserFormElements(form, func(v browserFormElementVisit) {
		e := v.element
		locked := v.effectiveReadOnly || e.EditableAdminOnly && !admin || v.conditional && (s.interp == nil || evaluationFailed)
		if states != nil {
			locked = locked || states.ReadOnly[v.path]
			for path, hidden := range states.Hidden {
				if hidden && (path == v.path || strings.HasPrefix(v.path, path+".")) {
					locked = true
				}
			}
		}
		if strings.EqualFold(e.DataPath, path) && !locked && e.Kind != metadata.FormElementEntityList {
			writable = true
		}
	})
	return writable
}

func (s *Server) signEntityListState(ctx context.Context, owner *metadata.Entity, form *metadata.FormModule, id string, values map[string]any) string {
	if len(listSources(form)) == 0 {
		return ""
	}
	v := map[string]string{}
	for path, name := range listSources(form) {
		if !choiceAttrMasked(s.fieldDecisions(ctx, owner), name) {
			v[path] = listSourceString(owner, form, path, listValue(values, name))
		}
	}
	schema, err := entityListSchema(form)
	if err != nil {
		return ""
	}
	st := entityListState{Owner: owner.Name, Form: form.Name, ID: id, User: entityListUser(ctx), Schema: schema, Expires: time.Now().Add(24 * time.Hour).Unix(), Values: v}
	b, err := json.Marshal(st)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(s.entityListStateMAC(b))
}
func (s *Server) parseEntityListState(ctx context.Context, raw string, owner *metadata.Entity, form *metadata.FormModule, id string) (map[string]string, error) {
	if len(raw) > 32<<10 {
		return nil, fmt.Errorf("list state too large")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf("list state required")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	mac, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(mac, s.entityListStateMAC(b)) {
		return nil, fmt.Errorf("invalid list state")
	}
	schema, schemaErr := entityListSchema(form)
	var st entityListState
	if schemaErr != nil || json.Unmarshal(b, &st) != nil || st.Owner != owner.Name || st.Form != form.Name || st.ID != id || st.User != entityListUser(ctx) || st.Schema != schema || st.Expires < time.Now().Unix() {
		return nil, fmt.Errorf("expired or mismatched list state")
	}
	return st.Values, nil
}
func listTypedValue(f *metadata.Field, v any) (any, bool, error) {
	if v == nil {
		return nil, true, nil
	}
	raw := strings.TrimSpace(listValueString(v))
	if raw == "" {
		return nil, true, nil
	}
	if f.RefEntity != "" {
		if _, id, ok := uuidFromValue(v); ok {
			return id, false, nil
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, false, err
		}
		return id, id == uuid.Nil, nil
	}
	switch f.Type {
	case metadata.FieldTypeString:
		return listValueString(v), false, nil
	case metadata.FieldTypeBool:
		if b, ok := v.(bool); ok {
			return b, false, nil
		}
		b, err := strconv.ParseBool(raw)
		return b, false, err
	case metadata.FieldTypeNumber:
		n, err := decimal.NewFromString(raw)
		return n, false, err
	case metadata.FieldTypeDate:
		if t, ok := v.(time.Time); ok {
			return t, t.IsZero(), nil
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
			if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
				return t, t.IsZero(), nil
			}
		}
	}
	return nil, false, fmt.Errorf("invalid list source")
}
func (s *Server) loadManagedEntityLists(ctx context.Context, owner *metadata.Entity, form *metadata.FormModule, values map[string]any, ownerID string, ids ...string) map[string]entityListData {
	if !hasManagedEntityLists(form) || s.reg == nil || s.store == nil || owner == nil {
		return nil
	}
	out := map[string]entityListData{}
	if err := metadata.ValidateFormEntityLists(owner, form, s.reg.Entities()); err != nil {
		return out
	}
	ownerDecision, ownerErr := s.rowDecision(ctx, owner, "read")
	if ownerID != "" {
		id, err := uuid.Parse(ownerID)
		if err != nil {
			ownerErr = err
		} else {
			row, err := s.store.GetByID(ctx, owner.Name, id, owner)
			if err != nil || row == nil || !s.rowAllowedContext(ctx, owner, "read", row) {
				ownerErr = fmt.Errorf("owner unavailable")
			}
		}
	}

	form.Walk(func(el *metadata.FormElement) bool {
		if el.Kind != metadata.FormElementEntityList || el.EntityList == nil {
			return true
		}
		if len(ids) > 0 && el.ID != ids[0] {
			return true
		}
		sp := el.EntityList
		dto := entityListData{Status: "denied", Columns: []string{}, Rows: []entityListRow{}}
		defer func() { out[el.ID] = dto }()
		if ownerErr != nil || !ownerDecision.Allowed {
			return true
		}
		target := s.reg.GetEntity(sp.Entity)
		decision, err := s.rowDecision(ctx, target, "read")
		if err != nil || !decision.Allowed {
			return true
		}
		fields := s.fieldDecisions(ctx, target)
		if choiceAttrMasked(fields, "id") || choiceAttrMasked(fields, "Ссылка") {
			return true
		}
		order := sp.OrderBy
		if len(order) == 0 {
			order = target.OrderBy
		}
		params := storage.ListParams{RowFilterEvaluated: true, Limit: sp.Limit, ExcludeFolders: target.Hierarchical, OrderKeys: []storage.ListOrderKey{}}
		if params.Limit == 0 {
			params.Limit = 5
		}
		if !decision.Unrestricted {
			params.RowFilter = decision.Predicate
		}
		for _, key := range order {
			name, desc := metadata.SplitOrderSpec(key)
			if choiceAttrMasked(fields, name) {
				return true
			}
			params.OrderKeys = append(params.OrderKeys, storage.ListOrderKey{Field: name, Desc: desc})
		}
		for _, c := range sp.Filter {
			f := metadata.EntityListField(target, c.Field)
			if choiceAttrMasked(fields, f.Name) {
				return true
			}
			var value any
			if c.Value != nil {
				value, err = metadata.EntityListLiteral(f, c.Value)
			} else {
				source := metadata.EntityListSourceField(owner, form, c.Source)
				if choiceAttrMasked(s.fieldDecisions(ctx, owner), source.Name) {
					return true
				}
				value = listValue(values, source.Name)
			}
			if err != nil {
				dto.Status = "error"
				return true
			}
			typed, empty, e := listTypedValue(f, value)
			if e != nil {
				dto.Status = "error"
				return true
			}
			if empty {
				dto.Status = "empty"
				return true
			}
			if f.RefEntity != "" {
				visible, e := s.choiceRefVisible(ctx, target, f.Name, typed.(uuid.UUID))
				if e != nil {
					dto.Status = "error"
					return true
				}
				if !visible {
					dto.Status = "empty"
					return true
				}
			}
			params.Equalities = append(params.Equalities, storage.ListEquality{Field: f.Name, Value: typed})
		}
		selected := []metadata.Field{}
		for _, name := range sp.Columns {
			f := metadata.EntityListField(target, name)
			d, ok := fieldDecisionByName(fields, f.Name)
			if ok && d.Hidden() {
				continue
			}
			dto.Columns = append(dto.Columns, f.Name)
			selected = append(selected, *f)
		}
		rows, e := s.store.List(ctx, target.Name, target, params)
		if e != nil {
			dto.Status = "error"
			return true
		}
		s.maskRecords(ctx, target, rows)
		// Resolve only projected references through target RBAC/RLS and field masks.
		for _, f := range selected {
			if f.RefEntity == "" {
				continue
			}
			ids := map[string]uuid.UUID{}
			for _, row := range rows {
				if key, id, ok := uuidFromValue(row[f.Name]); ok {
					ids[key] = id
				}
			}
			labels := map[string]string{}
			s.batchReadableLabels(ctx, s.reg.GetEntity(f.RefEntity), ids, labels)
			for _, row := range rows {
				if key, _, ok := uuidFromValue(row[f.Name]); ok {
					row[f.Name] = labels[key]
				}
			}
		}
		for _, row := range rows {
			r := entityListRow{Cells: []string{}, Marked: asBool(row["deletion_mark"])}
			for _, f := range selected {
				r.Cells = append(r.Cells, listValueString(row[f.Name]))
			}
			if sp.OnRowClick == "открыть" && !choiceAttrMasked(fields, "id") {
				if _, id, ok := uuidFromValue(row["id"]); ok {
					r.URL = "/ui/" + string(target.Kind) + "/" + url.PathEscape(target.Name) + "/" + id.String()
				}
			}
			dto.Rows = append(dto.Rows, r)
		}
		dto.Status = "ready"
		if len(dto.Rows) == 0 {
			dto.Status = "empty"
		}
		return true
	})
	if len(out) == 0 {
		return nil
	}
	return out
}

// Sources are search parameters, never authorization. Readonly existing-object
// values come from storage; new-object values require the server's signed state.
func (s *Server) entityListRefreshValues(r *http.Request, owner *metadata.Entity, form *metadata.FormModule, id string, sources map[string]string) (map[string]any, error) {
	values := map[string]any{}
	baseline := map[string]any{}
	if id != "" {
		uid, err := uuid.Parse(id)
		if err != nil || uid == uuid.Nil {
			return nil, fmt.Errorf("invalid owner id")
		}
		if !s.rowAllowsID(r.Context(), owner, "read", uid) {
			return nil, fmt.Errorf("denied")
		}
		row, err := s.store.GetByID(r.Context(), owner.Name, uid, owner)
		if err != nil || row == nil {
			return nil, fmt.Errorf("owner unavailable")
		}
		baseline = row
	} else {
		if !s.can(r, string(owner.Kind), owner.Name, "write") {
			return nil, fmt.Errorf("denied")
		}
	}
	signed, _ := s.parseEntityListState(r.Context(), r.FormValue("state"), owner, form, id)
	allowed := listSources(form)
	for path := range sources {
		if _, ok := allowed[path]; !ok {
			return nil, fmt.Errorf("unexpected source")
		}
	}
	for k, v := range baseline {
		values[k] = v
	}
	for path, name := range allowed {
		if choiceAttrMasked(s.fieldDecisions(r.Context(), owner), name) {
			continue
		}
		value := sources[path]
		if !s.listSourceWritable(owner, form, path, baseline, s.isAdmin(r)) {
			if strings.HasPrefix(strings.ToLower(path), strings.ToLower("Объект.")) && id != "" {
				setListValue(values, name, listValue(baseline, name))
				continue
			}
			v, ok := signed[path]
			if !ok {
				return nil, fmt.Errorf("readonly source requires server state")
			}
			value = v
		}
		setListValue(values, name, value)
	}
	return values, nil
}
func (s *Server) handleManagedListRefresh(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	fail := func(status int) {
		w.WriteHeader(status)
		respondJSON(enc, map[string]any{"ok": false, "error": "list unavailable"})
	}
	owner := s.getEntity(w, r)
	if owner == nil || string(owner.Kind) != chi.URLParam(r, "kind") || !s.can(r, string(owner.Kind), owner.Name, "read") {
		fail(http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		fail(http.StatusBadRequest)
		return
	}
	r = postFormOnlyRequest(r)
	form := findManagedFormByName(owner, r.FormValue("form"))
	if form == nil || metadata.ValidateFormEntityLists(owner, form, s.reg.Entities()) != nil {
		fail(http.StatusBadRequest)
		return
	}
	element := findChoiceElementByID(form, r.FormValue("element"))
	if element == nil || element.Kind != metadata.FormElementEntityList {
		fail(http.StatusBadRequest)
		return
	}
	var sources map[string]string
	if json.Unmarshal([]byte(r.FormValue("sources")), &sources) != nil {
		fail(http.StatusBadRequest)
		return
	}
	values, err := s.entityListRefreshValues(r, owner, form, r.FormValue("id"), sources)
	if err != nil {
		fail(http.StatusForbidden)
		return
	}
	lists := s.loadManagedEntityLists(r.Context(), owner, form, values, r.FormValue("id"), element.ID)
	respondJSON(enc, map[string]any{"ok": true, "entityLists": map[string]entityListData{element.ID: lists[element.ID]}})
}
