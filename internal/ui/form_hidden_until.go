package ui

// Карточка, скрытая до ответа на вопрос (как в 1С ПередОткрытием/
// ПриСозданииНаСервере): содержимое группы не показывается и не уезжает в
// браузер, пока условие hidden_when истинно.
//
//   - ПриСозданииНаСервере — серверный обработчик формы при GET, до отрисовки.
//     Реквизиты формы, которые он заполнил, участвуют в hidden_when первой же
//     отрисовки: содержимое не мелькает до события ПриОткрытии.
//   - Группа, скрытая условием при отрисовке, есть в разметке (display:none,
//     disabled): ответ события может её показать без перезагрузки. Поля внутри
//     disabled-группы браузер не отправляет, а запись формы отсутствующие поля
//     скрытой группы не затирает (form_partial_write.go).
//
// Значения полей, скрытых условием, ходят как раньше: секреты закрывает
// conceal (data-ob-conceal-context), а не hidden_when.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// hiddenWhenConstant — hidden_when, всегда истинный по записи: «Истина» или
// «true» (без учёта регистра и пробелов по краям). Так конфигурации держат
// служебные поля формы, никогда не показываемые пользователю.
func hiddenWhenConstant(expr string) bool {
	e := strings.TrimSpace(expr)
	return strings.EqualFold(e, "Истина") || strings.EqualFold(e, "true")
}

// formCreateHook — обработчик ПриСозданииНаСервере формы, если объявлен.
func formCreateHook(form *metadata.FormModule) (*ast.Program, *ast.ProcedureDecl) {
	if form == nil {
		return nil, nil
	}
	procName := resolveHandlerProc(form, "", string(metadata.FormEventOnCreateAtServer))
	if procName == "" {
		return nil, nil
	}
	program, ok := form.ProgramAST.(*ast.Program)
	if !ok || program == nil {
		return nil, nil
	}
	for _, proc := range program.Procedures {
		if strings.EqualFold(proc.Name.Literal, procName) {
			return program, proc
		}
	}
	return nil, nil
}

// runFormCreateHook исполняет ПриСозданииНаСервере при GET формы объекта, до
// отрисовки, под текущим пользователем. «Объект» — запись из базы (или новый
// объект); реквизиты формы обработчик читает и пишет как Объект.<Реквизит>, и
// их значения переносятся в значения отрисовки — а значит, в hidden_when и
// readonly_when первой отрисовки. Поля самого объекта, изменённые обработчиком,
// в отрисовку не переносятся: заполнение нового объекта делает ПриСозданииНового
// модуля объекта, изменение записи — события формы после открытия.
//
// Ошибка обработчика возвращается вызывающему: форма не отрисовывается
// (fail-closed), иначе содержимое, которое обработчик должен был скрыть, ушло
// бы в браузер.
func (s *Server) runFormCreateHook(ctx context.Context, entity *metadata.Entity, form *metadata.FormModule, data map[string]any) error {
	if form == nil || entity == nil || s.interp == nil {
		return nil
	}
	program, decl := formCreateHook(form)
	if program == nil || decl == nil {
		return nil
	}
	values, _ := data["Values"].(map[string]string)
	isNew, _ := data["IsNew"].(bool)
	var obj *runtime.Object
	if idStr, _ := data["ID"].(string); !isNew && idStr != "" {
		id, err := uuid.Parse(idStr)
		if err != nil {
			return fmt.Errorf("ПриСозданииНаСервере: некорректный идентификатор записи")
		}
		if obj, err = s.loadRuntimeObject(ctx, entity, id); err != nil {
			return fmt.Errorf("ПриСозданииНаСервере: не удалось загрузить объект: %w", err)
		}
		// Объект.Ссылка — ссылка открытой записи, как в событиях формы. У нового
		// объекта ссылка пуста (как в 1С до записи).
		setPersistedFormSelfRef(entity, obj)
	} else {
		obj = &runtime.Object{ID: uuid.New(), Type: entity.Name, Kind: entity.Kind,
			Fields: map[string]any{}, TablePartRows: map[string][]map[string]any{}}
	}
	for _, attr := range form.Attributes {
		if formAttrIsScalar(attr) {
			obj.Set(attr.Name, typeFormAttrValue(attr.TypeRef, values[attr.Name]))
		}
	}

	mc := runtime.NewMovementsCollector(entity.Name, obj.ID)
	var msgs []string
	vars, txState := s.buildDSLVarsWithMessagesTx(ctx, mc, &msgs)
	defer rollbackDSLExecution(txState)
	thisObj := s.newFormObjectThisLive(ctx, txState, obj, entity, form, isNew)
	vars["Объект"] = thisObj
	vars["ЭтотОбъект"] = thisObj
	addFormAttrVars(form, entity, thisObj, vars)
	formProcs := make(map[string]*ast.ProcedureDecl, len(program.Procedures))
	for _, p := range program.Procedures {
		formProcs[strings.ToLower(p.Name.Literal)] = p
	}
	vars["__form_procs__"] = formProcs
	if err := finishDSLExecution(txState, s.interp.Run(decl, thisObj, vars)); err != nil {
		return err
	}
	if values == nil {
		values = map[string]string{}
		data["Values"] = values
	}
	for _, attr := range form.Attributes {
		if formAttrIsScalar(attr) {
			values[attr.Name] = formCreateHookValue(obj.Get(attr.Name))
		}
	}
	return nil
}

// formCreateHookValue — значение реквизита формы строкой отрисовки.
func formCreateHookValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case time.Time:
		if t.IsZero() {
			return ""
		}
		return t.Format("2006-01-02T15:04:05")
	case *interpreter.Ref:
		if t == nil {
			return ""
		}
		return t.UUID
	}
	if interpreter.IsUndefined(v) {
		return ""
	}
	return fmt.Sprint(v)
}

// hiddenAncestor — скрыт ли условием при отрисовке какой-либо контейнер-предок
// размещения path (путь вида «2.0.1»: предки — «2» и «2.0»).
func hiddenAncestor(hidden map[string]bool, path string) bool {
	for i := 0; i < len(path); i++ {
		if path[i] == '.' && hidden[path[:i]] {
			return true
		}
	}
	return false
}
