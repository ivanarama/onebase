package ui

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
)

func (s *Server) addTabularDrilldownVars(r *http.Request, scope tabularScope, form *metadata.FormModule, element string, vars map[string]any) (err error) {
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
	if cell == nil || cell.Drilldown == nil {
		return fmt.Errorf("расшифровка недоступна")
	}
	target := s.reg.GetEntity(cell.Drilldown.Entity)
	id, e := uuid.Parse(cell.Drilldown.ID)
	if e != nil || target == nil || !s.can(r, string(target.Kind), target.Name, "read") || !s.rowAllowsID(r.Context(), target, "read", id) {
		return fmt.Errorf("расшифровка недоступна")
	}
	rootName := "Документы"
	if target.Kind == metadata.KindCatalog {
		rootName = "Справочники"
	}
	root, ok := vars[rootName].(interface{ Get(string) any })
	if !ok {
		return fmt.Errorf("расшифровка недоступна")
	}
	manager, _ := root.Get(target.Name).(interpreter.RefManager)
	rows, e := s.readableFieldsByIDs(r.Context(), target, []uuid.UUID{id}, displayField(target))
	if e != nil || rows[id.String()] == nil {
		return fmt.Errorf("расшифровка недоступна")
	}
	rowData := rows[id.String()]
	s.maskRecord(r.Context(), target, rowData)
	ref := &interpreter.Ref{UUID: id.String(), Type: target.Name, Kind: target.Kind, Name: firstStringField(rowData, target), Manager: manager}
	vars["Расшифровка"] = ref
	vars["Drilldown"] = ref
	return nil
}

func installTabularLayouts(vars map[string]any, root *tabularLayouts) {
	vars["Макеты"] = root
	vars["ПолучитьМакет"] = interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) { return root.CallMethod("Получить", args), nil })
}
