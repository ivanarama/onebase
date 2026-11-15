package ui

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/sheet"
)

func (s *Server) addTabularDrilldownVars(r *http.Request, scope tabularScope, form *metadata.FormModule, element string, vars map[string]any, resolver *dslRefAttrResolver) (err error) {
	// Native managers may raise a typed DSL error. Never turn failure into a ref.
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("расшифровка недоступна")
		}
	}()
	read := func(key string) string {
		if scope.Kind == "processor" {
			p := s.reg.GetProcessor(scope.Name)
			if p == nil {
				return ""
			}
			v, ok := processorPostFormValues(r, processorServiceFieldName(p.Params, key))
			if !ok || len(v) != 1 {
				return ""
			}
			return v[0]
		}
		values, ok := formPostValues(r, key)
		if !ok || len(values) != 1 {
			return ""
		}
		return values[0]
	}
	v, ok := s.readableTabular(r, read("_tabular_token"))
	if !ok || v.scope.Kind != scope.Kind || v.scope.Name != scope.Name || v.scope.ID != scope.ID || v.scope.Form != form.Name {
		return fmt.Errorf("расшифровка недоступна")
	}
	bound := false
	form.Walk(func(el *metadata.FormElement) bool {
		if el != nil && el.Kind == metadata.FormElementSpreadsheet && strings.EqualFold(el.Name, element) && strings.EqualFold(formDataPathFieldName(el.DataPath), v.scope.Field) {
			bound = true
		}
		return true
	})
	if !bound {
		return fmt.Errorf("расшифровка недоступна")
	}
	row, e1 := strconv.Atoi(read("_tabular_row"))
	col, e2 := strconv.Atoi(read("_tabular_col"))
	if e1 != nil || e2 != nil {
		return fmt.Errorf("расшифровка недоступна")
	}
	cell := v.doc.GetCell(row, col)
	if !cell.HasDrilldown() {
		return fmt.Errorf("расшифровка недоступна")
	}
	payload := cell.DrilldownPayload
	if payload == nil {
		payload = &sheet.DrilldownValue{Kind: "reference", Ref: cell.Drilldown}
	}
	// Resolve through the live DSL roots; never retain a generation-time manager.
	memo := map[string]*interpreter.Ref{}
	resolve := func(ref *sheet.Reference, kind string) (any, error) {
		target := s.reg.GetEntity(ref.Entity)
		if target == nil || (kind != "" && kind != string(target.Kind)) {
			return nil, fmt.Errorf("расшифровка недоступна")
		}
		key := strings.ToLower(target.Name) + "/" + ref.ID
		if cached := memo[key]; cached != nil {
			copy := *cached
			return &copy, nil
		}
		rootName := "Документы"
		if target.Kind == metadata.KindCatalog {
			rootName = "Справочники"
		}
		root, ok := vars[rootName].(interface{ Get(string) any })
		if !ok {
			return nil, fmt.Errorf("расшифровка недоступна")
		}
		manager, ok := root.Get(target.Name).(interpreter.RefManager)
		if !ok {
			return nil, fmt.Errorf("расшифровка недоступна")
		}
		if resolver == nil {
			return nil, fmt.Errorf("расшифровка недоступна")
		}
		result := &interpreter.Ref{Type: target.Name, Kind: target.Kind, Manager: manager, AttrResolver: resolver}
		if ref.ID == "" || ref.ID == uuid.Nil.String() {
			return result, nil
		}
		id, e := uuid.Parse(ref.ID)
		if e != nil || !s.can(r, string(target.Kind), target.Name, "read") {
			return nil, fmt.Errorf("расшифровка недоступна")
		}
		// The field projection already applies target row policy in SQL.
		rows, e := s.readableFieldsByIDs(r.Context(), target, []uuid.UUID{id}, displayField(target))
		if e != nil || rows[id.String()] == nil {
			return nil, fmt.Errorf("расшифровка недоступна")
		}
		row := rows[id.String()]
		s.maskRecord(r.Context(), target, row)
		result.UUID = id.String()
		result.Name = firstStringField(row, target)
		memo[key] = result
		copy := *result
		return &copy, nil
	}
	data, e := interpreter.RestoreTabularDrilldown(payload, resolve)
	if e != nil {
		return fmt.Errorf("расшифровка недоступна")
	}
	vars["Расшифровка"] = data
	vars["Drilldown"] = data
	return nil
}

func installTabularLayouts(vars map[string]any, root *tabularLayouts) {
	vars["Макеты"] = root
	vars["ПолучитьМакет"] = interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) { return root.CallMethod("Получить", args), nil })
}
