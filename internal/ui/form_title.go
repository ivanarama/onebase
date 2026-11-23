package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// managedFormTitle deliberately receives a copy: a display function must never
// change the form's save values. Database writes and external effects are denied.
func (s *Server) managedFormTitle(ctx context.Context, entity *metadata.Entity, form *metadata.FormModule, obj *runtime.Object) (*string, error) {
	if form == nil || strings.TrimSpace(form.TitleProc) == "" {
		return nil, nil
	}
	neutral := entity.DisplayName(langFromCtx(ctx))
	program, _ := form.ProgramAST.(*ast.Program)
	if program == nil || s.interp == nil || obj == nil {
		return &neutral, fmt.Errorf("form title evaluation failed")
	}
	var decl *ast.ProcedureDecl
	procs := make(map[string]*ast.ProcedureDecl)
	for _, p := range program.Procedures {
		procs[strings.ToLower(p.Name.Literal)] = p
		if strings.EqualFold(p.Name.Literal, form.TitleProc) {
			decl = p
		}
	}
	if decl == nil || len(decl.Params) != 0 {
		return &neutral, fmt.Errorf("form title evaluation failed")
	}
	ctx = storage.ReadOnlyContext(trustedDSLContext(ctx))
	snapshot := *obj
	snapshot.Fields = cloneRecord(obj.Fields)
	snapshot.TablePartRows = cloneTablePartRowMap(obj.TablePartRows)
	var messages []string
	vars, tx := s.buildDSLVarsWithMessagesTx(ctx, runtime.NewMovementsCollector(entity.Name, obj.ID), &messages)
	defer rollbackDSLExecution(tx)
	this := s.newFormObjectThisLive(ctx, tx, &snapshot, entity, form, obj.ID == uuid.Nil)
	this.writeBlocked = true
	vars["Объект"], vars["ЭтотОбъект"], vars["__form_procs__"] = this, this, procs
	addFormAttrVars(form, entity, this, vars)
	result, err := s.interp.CallEntrySandboxedWithBindings(decl, this, nil, interpreter.SandboxProfile{
		Context: ctx, DenyNet: true, DenyFile: true, DenyExec: true, DenySecrets: true, MaxWallClock: 3 * time.Second,
	}, vars)
	err = finishDSLExecution(tx, err)
	if err != nil {
		return &neutral, fmt.Errorf("form title evaluation failed")
	}
	title, ok := result.Value.(string)
	if !ok {
		return &neutral, fmt.Errorf("form title evaluation failed")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = neutral
	}
	return &title, nil
}

func titleObjectFromData(entity *metadata.Entity, data map[string]any) *runtime.Object {
	obj := runtime.NewObject(entity.Name, entity.Kind)
	obj.ID = uuid.Nil
	if _, id, ok := uuidFromValue(data["ID"]); ok {
		obj.ID = id
	}
	switch values := data["Values"].(type) {
	case map[string]any:
		obj.Fields = cloneRecord(values)
	case map[string]string:
		for key, value := range values {
			obj.Fields[key] = value
		}
	}
	for _, field := range entity.Fields {
		if value, ok := obj.Fields[field.Name].(string); ok {
			obj.Fields[field.Name] = typeFormAttrValue(string(field.Type), value)
		}
	}
	if form, ok := data["Form"].(*metadata.FormModule); ok {
		for _, attr := range form.Attributes {
			if formAttrIsScalar(attr) {
				if value, ok := obj.Fields[attr.Name].(string); ok {
					obj.Fields[attr.Name] = typeFormAttrValue(attr.TypeRef, value)
				}
			}
		}
	}
	if rows, ok := data["TablePartRows"].(map[string][]map[string]any); ok {
		obj.TablePartRows = cloneTablePartRowMap(rows)
	}
	return obj
}
