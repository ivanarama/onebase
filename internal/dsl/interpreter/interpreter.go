package interpreter

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/dsl/token"
	"github.com/shopspring/decimal"
)

// ErrDivisionByZero помечает ошибку деления на ноль. Доступна через
// errors.Is(err, ErrDivisionByZero) по цепочке DSLError.Unwrap. Нужна, чтобы
// контексты, где деление на ноль — это «неопределённое значение» (компоновка
// отчётов: пустая ячейка, как в 1С), отличали его от настоящих runtime-ошибок;
// при этом обычное исполнение DSL по-прежнему возбуждает явную ошибку.
var ErrDivisionByZero = errors.New("деление на ноль")

// dslStop — системная остановка (Error без Попытки, внутренние ошибки интерпретатора)
type dslStop struct{ err error }

// dslReturn — ранний выход через Возврат
type dslReturn struct{ val any }

// userError — пользовательская ошибка через Error(), перехватывается Попыткой.
// File/Line — место возбуждения (для ИнформацияОбОшибке); могут быть пустыми,
// если ошибка поднята из метода объекта (RaiseUserError) без позиции.
type userError struct {
	Msg  string
	File string
	Line int
	Err  error // исходная ошибка (например i18nerr) для локализации по цепочке
}

// RaiseUserError panics with a DSL user error. Предназначено для
// внешних пакетов (например ui), которым нужно прервать выполнение DSL
// из метода объекта (CallMethod) с осмысленным сообщением — оно
// перехватывается Run/RunWithResult и Попыткой так же, как Error().
func RaiseUserError(msg string) {
	panic(userError{Msg: msg})
}

// RaiseUserErrorWrap — как RaiseUserError, но сохраняет исходную error (i18nerr)
// в userError.Err → DSLError.Err, чтобы i18nerr.Localize локализовал сообщение
// по цепочке, а не показывал русский текст не-русскому пользователю.
func RaiseUserErrorWrap(msg string, err error) {
	panic(userError{Msg: msg, Err: err})
}

// loopBreak — выход из цикла через Прервать
type loopBreak struct{}

// loopContinue — переход к следующей итерации через Продолжить
type loopContinue struct{}

// DebugHook is the interface the interpreter calls for debugging.
// When nil on the Interpreter, there is zero overhead.
// Implemented by debugger.ActiveSession.
type DebugHook interface {
	// HookCheckBreakpoint отвечает, надо ли останавливаться на строке. cond
	// вычисляет условие точки останова в окружении текущего оператора и
	// приводит результат к булеву по правилам `Если`; хук зовёт его только
	// когда на строке есть включённая точка с непустым условием.
	HookCheckBreakpoint(file string, line int, cond func(expr string) (bool, error)) bool
	HookShouldStep(file string, stackDepth int) bool
	HookOnPause(file string, line int, vars map[string]any, evalFn func(string) (any, error), reason string)
	HookPushFrame(procedure string, line int)
	HookPopFrame()
}

type Interpreter struct {
	LookupProc func(name string) *ast.ProcedureDecl
	// LookupSiblingProc resolves a helper procedure defined in the same
	// source file as the currently-executing statement. Used so that
	// `.proc.os` / `.posting.os` / `.rep.os` могут содержать вспомогательные
	// процедуры (см. Optional — может быть nil.
	LookupSiblingProc func(file, name string) *ast.ProcedureDecl
	// LookupModuleProc resolves Module.Proc() namespaced calls, например
	// `Утилиты.ФИФО(...)`. Используется когда object-часть MemberExpr —
	// идентификатор, не разрешённый в env как переменная. См.
	LookupModuleProc func(module, name string) *ast.ProcedureDecl
	// DebugSource выдаёт debug hook для очередного запуска (nil = без отладки).
	// Захватывается один раз на Run/Call/RunWithResult в execCtx запуска.
	// Устанавливается однократно при конфигурировании сервера (как LookupProc);
	// текущее включён/выключен живёт внутри источника (GlobalDebugController),
	// поэтому Interpreter после старта неизменяем и безопасен для конкурентных
	// запусков (план 52: раньше поле DebugHook мутировалось хендлерами на лету).
	DebugSource func() DebugHook
	// MaxRecursionDepth ограничивает глубину вложенных вызовов процедур/функций.
	// 0 = defaultMaxRecursionDepth. Поле (а не глобальная константа), чтобы порог
	// можно было задать per-Interpreter и понизить в тестах стража рекурсии.
	MaxRecursionDepth int
	// MaxEvalDepth ограничивает глубину вложенных Вычислить/Eval, которая не
	// увеличивает MaxRecursionDepth. 0 = defaultMaxEvalDepth.
	MaxEvalDepth int
	// StrictLexicalScope включает opt-in режим, где вызванная процедура видит
	// только свои параметры/локальные переменные и root-env запуска (extraVars,
	// factories, This), но не локальные переменные caller-процедуры.
	StrictLexicalScope bool
}

// startEnv создаёт корневое окружение запуска и захватывает debug hook
// из DebugSource в его execCtx.
func (i *Interpreter) startEnv(this This) *env {
	e := newEnv(this)
	installEnvironmentConstants(e)
	if i.DebugSource != nil {
		e.ec.debug = i.DebugSource()
	}
	return e
}

func New() *Interpreter { return &Interpreter{} }

// EvalExpr evaluates a parsed AST expression and returns the result.
// Public for the debugger console and debug handlers.
func (i *Interpreter) EvalExpr(expr ast.Expr, this This) any {
	e := i.startEnv(this)
	return i.evalExpr(expr, e)
}

// Call executes a procedure with positional arguments and captures the return
// value. Используется для вызова процедур модуля менеджера через
// Документы/Справочники.X.Method(args…) — args биндятся на proc.Params
// через callUserProc (включая обработку дефолтов).
func (i *Interpreter) Call(proc *ast.ProcedureDecl, this This, args []any, extraVars ...map[string]any) (result any, err error) {
	e := i.startEnv(this)
	if proc != nil {
		e.sourceFile = proc.Name.File
	}
	defer func() {
		if r := recover(); r != nil {
			switch s := r.(type) {
			case dslStop:
				err = s.err
			case userError:
				err = &DSLError{File: e.ec.curFile, Line: e.ec.curLine, Msg: s.Msg, Err: s.Err}
			default:
				panic(r)
			}
		}
	}()
	for _, m := range extraVars {
		for k, v := range m {
			e.setLocal(k, v)
		}
	}
	result = i.callUserProc(proc, e, args)
	return
}

// RunWithResult executes a function procedure and captures its return value.
func (i *Interpreter) RunWithResult(proc *ast.ProcedureDecl, this This, result *any, extraVars ...map[string]any) (err error) {
	e := i.startEnv(this)
	if proc != nil {
		e.sourceFile = proc.Name.File
	}
	defer func() {
		if r := recover(); r != nil {
			switch s := r.(type) {
			case dslStop:
				err = s.err
			case userError:
				err = &DSLError{File: e.ec.curFile, Line: e.ec.curLine, Msg: s.Msg, Err: s.Err}
			case dslReturn:
				if result != nil {
					*result = s.val
				}
			default:
				panic(r)
			}
		}
	}()
	for _, m := range extraVars {
		for k, v := range m {
			e.setLocal(k, v)
		}
	}
	if i.StrictLexicalScope {
		if result != nil {
			*result = i.callEntryProc(proc, e, nil)
		} else {
			i.callEntryProc(proc, e, nil)
		}
		return nil
	}
	i.execBlock(proc.Body, e)
	return nil
}

// Run executes a procedure. Optional extra vars (e.g. {"Движения": collector}) are
// injected into the top-level environment.
func (i *Interpreter) Run(proc *ast.ProcedureDecl, this This, extraVars ...map[string]any) (err error) {
	e := i.startEnv(this)
	if proc != nil {
		e.sourceFile = proc.Name.File
	}
	defer func() {
		if r := recover(); r != nil {
			switch s := r.(type) {
			case dslStop:
				err = s.err
			case userError:
				err = &DSLError{File: e.ec.curFile, Line: e.ec.curLine, Msg: s.Msg, Err: s.Err}
			case dslReturn:
				// early return from procedure — not an error
			default:
				panic(r)
			}
		}
	}()
	for _, m := range extraVars {
		for k, v := range m {
			e.setLocal(k, v)
		}
	}
	if i.StrictLexicalScope {
		i.callEntryProc(proc, e, nil)
		return nil
	}
	i.execBlock(proc.Body, e)
	return nil
}

func (i *Interpreter) execBlock(stmts []ast.Stmt, e *env) {
	for _, s := range stmts {
		e.ec.checkDeadline()
		if loc := getLocation(s); loc != nil {
			e.ec.curFile = loc.File
			e.ec.curLine = loc.Line
		}
		if e.ec.debug != nil {
			i.beforeStmt(s, e)
		}
		i.execStmt(s, e)
		e.ec.checkDeadline()
	}
}

// execLoopBody runs a loop body and returns true if the loop should continue,
// false if Прервать was encountered. Продолжить causes early return to next iteration.
func (i *Interpreter) execLoopBody(body []ast.Stmt, e *env) (cont bool) {
	cont = true
	defer func() {
		if r := recover(); r != nil {
			switch r.(type) {
			case loopBreak:
				cont = false
			case loopContinue:
				// cont stays true, body was interrupted but loop continues
			default:
				panic(r)
			}
		}
	}()
	i.execBlock(body, e)
	return
}

func (i *Interpreter) beforeStmt(s ast.Stmt, e *env) {
	loc := getLocation(s)
	if loc == nil {
		return
	}

	hook := e.ec.debug
	hitBP := hook.HookCheckBreakpoint(loc.File, loc.Line, func(expr string) (bool, error) {
		// Условие — единственное отладочное выражение, которое исполняется без
		// участия человека и на каждом проходе строки, поэтому вычисляется в
		// read-only песочнице (#883). Табло и консоль остаются полноценными:
		// их человек набирает сам и видит результат.
		restore := withConditionLimits(e)
		defer restore()
		v, err := i.evalDebugExpr(expr, e)
		if err != nil {
			return false, err
		}
		return truthy(v), nil
	})
	shouldStep := hook.HookShouldStep(loc.File, stackDepth(e))
	if !hitBP && !shouldStep {
		return
	}

	reason := "step"
	if hitBP {
		reason = "breakpoint"
	}
	vars := e.GetAllVariables()
	evalFn := func(expr string) (any, error) {
		return i.evalDebugExpr(expr, e)
	}
	hook.HookOnPause(loc.File, loc.Line, vars, evalFn, reason)
}

// evalDebugExpr вычисляет выражение, которое человек написал в отладчике
// (условие точки останова, табло, консоль), в окружении остановленного
// оператора. От evaluateExprString отличается двумя вещами:
//
//   - любая паника интерпретатора превращается в error. Выражение отладчика —
//     не часть конфигурации, и опечатка в нём не должна ронять отлаживаемый
//     прогон: раньше паника уходила вверх через beforeStmt и убивала проведение
//     документа. Для условия это критично — оно вычисляется на каждом проходе
//     строки, без участия человека;
//   - позиция последнего оператора (ec.curFile/curLine) восстанавливается.
//     Вычисление может вызвать процедуру из другого модуля и увести позицию за
//     собой, после чего ошибка основного кода показала бы чужой файл и строку.
func (i *Interpreter) evalDebugExpr(expr string, e *env) (res any, err error) {
	savedFile, savedLine := e.ec.curFile, e.ec.curLine
	savedDebug := e.ec.debug
	// The guard belongs to this execution context, not to the shared debug
	// session. A debugger expression may execute DSL itself; disabling the hook
	// here prevents recursive breakpoints/steps in this run without making
	// concurrent runs silently skip their own breakpoints.
	e.ec.debug = nil
	defer func() {
		e.ec.curFile, e.ec.curLine = savedFile, savedLine
		e.ec.debug = savedDebug
		if r := recover(); r != nil {
			res = nil
			switch v := r.(type) {
			case dslStop:
				err = v.err
			case userError:
				// Позиция может быть пустой (RaiseUserError из метода объекта) —
				// тогда показываем строку, на которой стоит отладчик.
				file, line := v.File, v.Line
				if file == "" {
					file, line = savedFile, savedLine
				}
				err = &DSLError{File: file, Line: line, Msg: v.Msg, Err: v.Err}
			default:
				err = fmt.Errorf("%v", r)
			}
		}
	}()
	return i.evaluateExprString(expr, e)
}

func stackDepth(e *env) int {
	if e != nil && e.depth > 0 {
		return e.depth
	}
	d := 0
	for e != nil {
		d++
		e = e.parent
	}
	return d
}

func (i *Interpreter) evaluateExprString(expr string, e *env) (any, error) {
	if e != nil && e.ec != nil && (e.ec.maxDecimalExpansion > 0 || e.ec.maxStringExpansion > 0 || e.ec.readOnlyReason != "") {
		if err := ValidateUntrustedExpressionSource(expr); err != nil {
			return nil, err
		}
	}
	l := lexer.New(expr, "<console>")
	p := parser.New(l)
	parsed, err := p.ParseStandaloneExpr()
	if err != nil {
		return nil, err
	}
	return i.evalExpr(parsed, e), nil
}

func (i *Interpreter) execStmt(s ast.Stmt, e *env) {
	switch v := s.(type) {
	case *ast.IfStmt:
		// Управляющие блоки НЕ создают дочерний scope: областью видимости
		// переменной в DSL onebase является процедура/функция целиком (как
		// в 1С), а не блок. См. П.39 — иначе переменная, впервые присвоенная
		// внутри Если/цикла, была бы локальной к блоку и тихо терялась.
		cond := i.evalExpr(v.Cond, e)
		if truthy(cond) {
			i.execBlock(v.Then, e)
		} else {
			matched := false
			for _, elif := range v.ElseIfs {
				if truthy(i.evalExpr(elif.Cond, e)) {
					i.execBlock(elif.Body, e)
					matched = true
					break
				}
			}
			if !matched && len(v.Else) > 0 {
				i.execBlock(v.Else, e)
			}
		}
	case *ast.ForEachStmt:
		coll := i.evalExpr(v.Collection, e)
		switch items := coll.(type) {
		case []map[string]any:
			for _, row := range items {
				e.setLocal(v.Var.Literal, &MapThis{M: row})
				if !i.execLoopBody(v.Body, e) {
					break
				}
			}
		case []any:
			for _, item := range items {
				e.setLocal(v.Var.Literal, item)
				if !i.execLoopBody(v.Body, e) {
					break
				}
			}
		case *Array:
			for _, item := range items.Iterate() {
				e.setLocal(v.Var.Literal, item)
				if !i.execLoopBody(v.Body, e) {
					break
				}
			}
		case *Map:
			for idx, key := range items.keys {
				e.setLocal(v.Var.Literal, &KeyValue{Key: key, Value: items.vals[idx]})
				if !i.execLoopBody(v.Body, e) {
					break
				}
			}
		case interface{ IterateThis() []This }:
			// IterateThis/IterateRows are arbitrary Go callbacks, even though the
			// DSL surface makes them look like passive collections. Treat them as
			// object capabilities in read-only debugger conditions.
			refuseReadOnly(e.ec, "итерация внешней коллекции")
			for _, item := range items.IterateThis() {
				e.setLocal(v.Var.Literal, item)
				if !i.execLoopBody(v.Body, e) {
					break
				}
			}
		default:
			// Поддержка прокси-объектов вроде *formTpProxy: если у значения
			// есть метод IterateRows() — итерируемся по нему. Без этого
			// `Для Каждого Стр Из Объект.Товары` ничего не делает, когда
			// `Объект.Товары` возвращает прокси для модификации ТЧ через
			// .Добавить()/.Очистить().
			if it, ok := coll.(interface{ IterateRows() []map[string]any }); ok {
				refuseReadOnly(e.ec, "итерация внешней коллекции")
				for _, row := range it.IterateRows() {
					e.setLocal(v.Var.Literal, &MapThis{M: row})
					if !i.execLoopBody(v.Body, e) {
						break
					}
				}
			}
		}
	case *ast.AssignStmt:
		val := i.evalExpr(v.Value, e)
		if v.Op != token.ASSIGN && v.Op != 0 {
			old := i.evalExpr(v.Target, e)
			val = applyCompoundOp(v.Op, old, val, e.ec)
		}
		i.assign(v.Target, val, e)
	case *ast.ExprStmt:
		i.evalExpr(v.X, e)
	case *ast.VarDecl:
		for _, n := range v.Names {
			if v.ModuleLevel {
				e.declareModule(n.Literal, nil)
			} else {
				e.declare(n.Literal, nil)
			}
		}
	case *ast.NumericForStmt:
		start := toFloatOr0(i.evalExpr(v.Start, e))
		end := toFloatOr0(i.evalExpr(v.End, e))
		iter := 0
		for counter := start; counter <= end; counter++ {
			iter++
			e.ec.checkDeadline()
			if iter > e.ec.loopLimit() {
				if e.ec.maxLoopIters > 0 {
					panic(dslStop{err: errSandboxIters})
				}
				RaiseUserError("Цикл «Для»: превышено максимальное число итераций — вероятно, ошибка в границах цикла")
			}
			e.setLocal(v.Var.Literal, counter)
			if !i.execLoopBody(v.Body, e) {
				break
			}
		}
	case *ast.WhileStmt:
		// Защита от зацикливания: сессия onebase однопоточная, runaway-цикл
		// заблокировал бы всю работу платформы. Лимит — см. limits.go.
		iter := 0
		for truthy(i.evalExpr(v.Cond, e)) {
			iter++
			e.ec.checkDeadline()
			if iter > e.ec.loopLimit() {
				if e.ec.maxLoopIters > 0 {
					panic(dslStop{err: errSandboxIters})
				}
				RaiseUserError("Цикл «Пока»: превышено максимальное число итераций — вероятно, бесконечный цикл")
			}
			if !i.execLoopBody(v.Body, e) {
				break
			}
		}
	case *ast.ReturnStmt:
		var val any
		if v.Value != nil {
			val = i.evalExpr(v.Value, e)
		}
		e.ec.checkDeadline()
		panic(dslReturn{val: val})
	case *ast.TryStmt:
		i.execTry(v, e)
	case *ast.BreakStmt:
		panic(loopBreak{})
	case *ast.ContinueStmt:
		panic(loopContinue{})
	}
}

func (i *Interpreter) assign(target ast.Expr, val any, e *env) {
	switch t := target.(type) {
	case *ast.Ident:
		e.set(t.Tok.Literal, val)
	case *ast.MemberExpr:
		refuseReadOnly(e.ec, "изменение свойства «"+t.Field.Literal+"»")
		obj := i.evalExpr(t.Object, e)
		field := strings.ToLower(t.Field.Literal)
		switch o := obj.(type) {
		case This:
			o.Set(field, val)
		case *Map:
			// Симметрично чтению: запись по точке у Соответствия не работает —
			// раньше тихо терялась, теперь явная ошибка с подсказкой.
			RaiseUserError("Соответствие не поддерживает запись по точке «." + t.Field.Literal +
				"» — используйте Вставить(\"" + t.Field.Literal + "\", Значение)")
		}
	case *ast.IndexExpr:
		refuseReadOnly(e.ec, "изменение элемента коллекции")
		obj := i.evalExpr(t.Object, e)
		idx := i.evalExpr(t.Index, e)
		switch o := obj.(type) {
		case *Array:
			o.SetIndex(int(toFloatOr0(idx)), val)
		case *Map:
			o.CallMethod("вставить", []any{idx, val})
		}
	}
}

func (i *Interpreter) evalExpr(expr ast.Expr, e *env) any {
	result := i.evalExprUnchecked(expr, e)
	e.ec.checkReadOnlyViolation()
	return result
}

func (i *Interpreter) evalExprUnchecked(expr ast.Expr, e *env) any {
	switch v := expr.(type) {
	case *ast.StringLit:
		return v.Value
	case *ast.NumberLit:
		d, err := decimal.NewFromString(v.Value)
		if err != nil {
			return decimal.Zero
		}
		return d
	case *ast.BoolLit:
		return v.Value
	case *ast.Ident:
		val, _ := e.get(v.Tok.Literal)
		return val
	case *ast.MemberExpr:
		obj := i.evalExpr(v.Object, e)
		field := strings.ToLower(v.Field.Literal)
		switch o := obj.(type) {
		case This:
			return protectReadOnly(e.ec, o.Get(field))
		case *Ref:
			return protectReadOnly(e.ec, o.Get(field))
		case *Map:
			// Соответствие не поддерживает чтение по точке (как в 1С) — частая
			// ошибка с результатом ПрочитатьJSON. Раньше тихо возвращали
			// Неопределено, что прятало опечатку; теперь — понятная ошибка.
			RaiseUserError("Соответствие не поддерживает чтение по точке «." + v.Field.Literal +
				"» — используйте Получить(\"" + v.Field.Literal + "\")")
		}
		return nil
	case *ast.IndexExpr:
		obj := i.evalExpr(v.Object, e)
		idx := i.evalExpr(v.Index, e)
		switch o := obj.(type) {
		case *Array:
			return protectReadOnly(e.ec, o.Index(int(toFloatOr0(idx))))
		case *Map:
			return protectReadOnly(e.ec, o.CallMethod("получить", []any{idx}))
		}
		return nil
	case *ast.ArrayLit:
		items := make([]any, 0, len(v.Elements))
		for _, elem := range v.Elements {
			items = append(items, i.evalExpr(elem, e))
		}
		return NewArray(items)
	case *ast.NewExpr:
		return i.evalNew(v, e)
	case *ast.UnaryExpr:
		return i.evalUnary(v, e)
	case *ast.TernaryExpr:
		if truthy(i.evalExpr(v.Cond, e)) {
			return i.evalExpr(v.True, e)
		}
		return i.evalExpr(v.False, e)
	case *ast.BinaryExpr:
		return i.evalBinary(v, e)
	case *ast.CallExpr:
		return i.evalCall(v, e)
	case *ast.MissingArg:
		// Страховка инварианта. Пропуск разбирается в evalArgsKeepMissing, до
		// общего вычисления выражений, и сюда попасть не должен; если всё же
		// попал — это Неопределено, а не внутренний sentinel: наружу тип
		// интерпретатора не выходит ни по какому пути.
		return nil
	}
	return nil
}

func (i *Interpreter) evalNew(n *ast.NewExpr, e *env) any {
	args := i.evalArgs(n.Args, e)
	typeName := strings.ToLower(n.TypeName.Literal)
	switch typeName {
	case "массив", "array":
		return &Array{}
	case "соответствие", "map":
		return &Map{}
	case "структура", "structure":
		return newStruct(args)
	case "таблицазначений", "valuetable":
		return NewValueTable(args)
	case "регекс", "regex":
		// План 124. Встроенным типом, а не фабрикой окружения: фабрики
		// инжектируются точечно, и объект был бы доступен в форме, но не в
		// регламентном задании.
		return NewRegexObject(args)
	case "шаблонhtml", "htmltemplate":
		// План 125, по той же причине встроенным типом.
		return NewHTMLTemplateObject(args)
	}
	// Расширяемые типы через env: "__factory_<ИмяТипа>"
	if factory, ok := e.get("__factory_" + typeName); ok {
		if fn, ok := factory.(func([]any) any); ok {
			refuseReadOnly(e.ec, "создание объекта «"+n.TypeName.Literal+"» через внешнюю фабрику")
			return fn(args)
		}
	}
	panic(userError{Msg: "Новый: неизвестный тип " + n.TypeName.Literal})
}

func (i *Interpreter) evalUnary(u *ast.UnaryExpr, e *env) any {
	val := i.evalExpr(u.Operand, e)
	if e.ec != nil {
		requireSafeSandboxValueLimits(u.Op.Literal, []any{val}, e.ec.maxDecimalExpansion, e.ec.maxStringExpansion, u.Op.Line)
	}
	switch u.Op.Type {
	case token.NOT:
		return !truthy(val)
	case token.MINUS:
		requireSafeSandboxDecimalOperand(e.ec, u.Op.Literal, val, u.Op.Line)
		f, _ := toFloat(val)
		return -f
	}
	return nil
}

func (i *Interpreter) evalBinary(b *ast.BinaryExpr, e *env) any {
	// short-circuit для AND/OR
	if b.Op.Type == token.AND {
		l := i.evalExpr(b.Left, e)
		if !truthy(l) {
			return false
		}
		return truthy(i.evalExpr(b.Right, e))
	}
	if b.Op.Type == token.OR {
		l := i.evalExpr(b.Left, e)
		if truthy(l) {
			return true
		}
		return truthy(i.evalExpr(b.Right, e))
	}
	l := i.evalExpr(b.Left, e)
	r := i.evalExpr(b.Right, e)
	if e.ec != nil {
		// Apply input bounds before toDecimal or fmt can turn a short exponent
		// or an oversized injected string into a large allocation.
		requireSafeSandboxValueLimits(b.Op.Literal, []any{l, r}, e.ec.maxDecimalExpansion, e.ec.maxStringExpansion, b.Op.Line)
	}
	switch b.Op.Type {
	case token.ASSIGN: // equality in conditions
		return equalOperator(l, r, e.ec, b.Op.Line)
	case token.NEQ:
		return !equalOperator(l, r, e.ec, b.Op.Line)
	case token.LT:
		return compareOperator(l, r, e.ec, b.Op.Line) < 0
	case token.GT:
		return compareOperator(l, r, e.ec, b.Op.Line) > 0
	case token.LTE:
		return compareOperator(l, r, e.ec, b.Op.Line) <= 0
	case token.GTE:
		return compareOperator(l, r, e.ec, b.Op.Line) >= 0
	case token.PLUS:
		// Дата + Число → сдвиг на N секунд (семантика 1С/OneScript).
		if lt, ok := l.(time.Time); ok {
			requireSafeSandboxDecimalOperand(e.ec, b.Op.Literal, r, b.Op.Line)
			if sec, ok2 := toFloat(r); ok2 {
				return dateAddSeconds(lt, sec)
			}
			if isNumeric(r) {
				RaiseUserError("число для сдвига даты вне безопасного диапазона")
			}
		}
		if rt, ok := r.(time.Time); ok {
			requireSafeSandboxDecimalOperand(e.ec, b.Op.Literal, l, b.Op.Line)
			if sec, ok2 := toFloat(l); ok2 {
				return dateAddSeconds(rt, sec)
			}
			if isNumeric(l) {
				RaiseUserError("число для сдвига даты вне безопасного диапазона")
			}
		}
		// Строка + Строка — ВСЕГДА конкатенация, даже если обе строки состоят
		// из одних цифр ("0" + "7" → "07", а не 7). Раньше toDecimal приводил
		// числовые строки к числам и складывал их: ломалась доливка ведущих
		// нулей и любая сборка идентификаторов из цифровых фрагментов (issue
		// #316). Числовые поля из БД приходят в DSL как decimal.Decimal (см.
		// storage.normalizeNumber), поэтому арифметика `Объект.Сумма + 100`
		// не затрагивается — гейт срабатывает только на настоящих строках.
		_, lStr := l.(string)
		_, rStr := r.(string)
		if !lStr || !rStr {
			requireSafeSandboxDecimalOperand(e.ec, b.Op.Literal, l, b.Op.Line)
			requireSafeSandboxDecimalOperand(e.ec, b.Op.Literal, r, b.Op.Line)
			ld, lok := toDecimal(l)
			rd, rok := toDecimal(r)
			if lok && rok {
				requireSafeSandboxDecimalOperation(e.ec, b.Op.Literal, ld, b.Op.Line)
				requireSafeSandboxDecimalOperation(e.ec, b.Op.Literal, rd, b.Op.Line)
				return safeSandboxDecimalResult(e.ec, b.Op.Literal, ld.Add(rd), b.Op.Line)
			}
			// nil-toleration: пустое число + N → N, иначе `Объект.Сумма + 100`
			// при пустом поле дало бы concat «<nil>100», который потом ломает
			// запись в numeric (SQLSTATE 22P02).
			if l == nil && rok {
				return safeSandboxDecimalResult(e.ec, b.Op.Literal, rd, b.Op.Line)
			}
			if r == nil && lok {
				return safeSandboxDecimalResult(e.ec, b.Op.Literal, ld, b.Op.Line)
			}
		}
		return sandboxConcatValues(e.ec, b.Op.Literal, l, r, b.Op.Line)
	case token.MINUS:
		// Дата - Дата → разница в секундах; Дата - Число → сдвиг назад.
		if lt, ok := l.(time.Time); ok {
			if rt, ok2 := r.(time.Time); ok2 {
				return lt.Sub(rt).Seconds()
			}
			requireSafeSandboxDecimalOperand(e.ec, b.Op.Literal, r, b.Op.Line)
			if sec, ok2 := toFloat(r); ok2 {
				return dateAddSeconds(lt, -sec)
			}
			if isNumeric(r) {
				RaiseUserError("число для сдвига даты вне безопасного диапазона")
			}
		}
		requireSafeSandboxDecimalOperand(e.ec, b.Op.Literal, l, b.Op.Line)
		requireSafeSandboxDecimalOperand(e.ec, b.Op.Literal, r, b.Op.Line)
		ld, lok := toDecimal(l)
		rd, rok := toDecimal(r)
		if lok {
			requireSafeSandboxDecimalOperation(e.ec, b.Op.Literal, ld, b.Op.Line)
		}
		if rok {
			requireSafeSandboxDecimalOperation(e.ec, b.Op.Literal, rd, b.Op.Line)
		}
		if lok && rok {
			return safeSandboxDecimalResult(e.ec, b.Op.Literal, ld.Sub(rd), b.Op.Line)
		}
		if l == nil && rok {
			return safeSandboxDecimalResult(e.ec, b.Op.Literal, rd.Neg(), b.Op.Line)
		}
		if r == nil && lok {
			return safeSandboxDecimalResult(e.ec, b.Op.Literal, ld, b.Op.Line)
		}
	case token.STAR:
		requireSafeSandboxDecimalOperand(e.ec, b.Op.Literal, l, b.Op.Line)
		requireSafeSandboxDecimalOperand(e.ec, b.Op.Literal, r, b.Op.Line)
		ld, lok := toDecimal(l)
		rd, rok := toDecimal(r)
		if lok && rok {
			requireSafeSandboxDecimalOperation(e.ec, b.Op.Literal, ld, b.Op.Line)
			requireSafeSandboxDecimalOperation(e.ec, b.Op.Literal, rd, b.Op.Line)
			return safeSandboxDecimalResult(e.ec, b.Op.Literal, ld.Mul(rd), b.Op.Line)
		}
		// nil * число / число * nil → 0 (а не string concat).
		if (l == nil && rok) || (r == nil && lok) {
			return decimal.Zero
		}
	case token.PERCENT:
		requireSafeSandboxDecimalOperand(e.ec, b.Op.Literal, l, b.Op.Line)
		requireSafeSandboxDecimalOperand(e.ec, b.Op.Literal, r, b.Op.Line)
		ld, lok := toDecimal(l)
		rd, rok := toDecimal(r)
		if lok {
			requireSafeSandboxDecimalOperation(e.ec, b.Op.Literal, ld, b.Op.Line)
		}
		if rok {
			requireSafeSandboxDecimalOperation(e.ec, b.Op.Literal, rd, b.Op.Line)
		}
		// Остаток десятичный, без усечения операндов: 7.5 % 2 = 1.5.
		// Условие деления на ноль такое же, как у SLASH: ошибка возникает,
		// только когда операция вообще применима — иначе «abc % 0» падал бы
		// делением на ноль там, где обычное деление возвращает Неопределено.
		if rok && rd.IsZero() && (lok || l == nil) {
			panic(userError{Msg: "Деление на ноль", Line: b.Op.Line, Err: ErrDivisionByZero})
		}
		if lok && rok {
			requireSafeDecimalQuotient(ld, rd, b.Op.Line)
			return safeSandboxDecimalResult(e.ec, b.Op.Literal, ld.Mod(rd), b.Op.Line)
		}
		if l == nil && rok {
			return decimal.Zero
		}
	case token.SLASH:
		requireSafeSandboxDecimalOperand(e.ec, b.Op.Literal, l, b.Op.Line)
		requireSafeSandboxDecimalOperand(e.ec, b.Op.Literal, r, b.Op.Line)
		ld, lok := toDecimal(l)
		rd, rok := toDecimal(r)
		if lok {
			requireSafeSandboxDecimalOperation(e.ec, b.Op.Literal, ld, b.Op.Line)
		}
		if rok {
			requireSafeSandboxDecimalOperation(e.ec, b.Op.Literal, rd, b.Op.Line)
		}
		// Деление на ноль — исключение (как в 1С), а не молчаливый nil. Err несёт
		// сентинел ErrDivisionByZero, чтобы компоновка отчётов отличила его от
		// настоящей runtime-ошибки (там это «неопределённое значение» → пустая ячейка).
		if rok && rd.IsZero() && (lok || l == nil) {
			panic(userError{Msg: "Деление на ноль", Line: b.Op.Line, Err: ErrDivisionByZero})
		}
		if lok && rok {
			requireSafeDecimalQuotient(ld, rd, b.Op.Line)
			return safeSandboxDecimalResult(e.ec, b.Op.Literal, ld.Div(rd), b.Op.Line)
		}
		if l == nil && rok {
			return decimal.Zero
		}
	}
	return nil
}

func (i *Interpreter) evalCall(c *ast.CallExpr, e *env) any {
	// Два списка на один вызов: пользовательская процедура получает пропуски
	// (userArgs) и подставляет по ним значения по умолчанию параметров, все
	// остальные приёмники — уже нормализованные в Неопределено (args). Куда
	// именно уйдёт вызов, выясняется ниже, а вычислять аргументы дважды нельзя:
	// у них бывают побочные эффекты.
	userArgs := i.evalArgsKeepMissing(c.Args, e)
	args := clearMissingArgs(userArgs)
	switch callee := c.Callee.(type) {
	case *ast.Ident:
		fnName := callee.Tok.Literal
		lowName := strings.ToLower(fnName)
		var fallback FallbackBuiltinFunc
		// Обычный AST всегда несёт identity собственного исходника в токене.
		// Только выражения, разобранные динамически для Вычислить/отладчика,
		// наследуют lexical identity текущего кадра.
		sourceFile := callSourceFile(callee.Tok.File, e)
		// Вычислить(Выражение) — разбор строки как выражения и вычисление в
		// текущем окружении (видит локальные переменные). Обрабатывается до
		// обычного поиска builtin, т.к. требует доступа к env.
		if lowName == "вычислить" || lowName == "eval" {
			hasResourceLimits := e.ec != nil && (e.ec.maxDecimalExpansion > 0 || e.ec.maxStringExpansion > 0)
			if hasResourceLimits {
				requireSafeSandboxBuiltinInputs(lowName, args, e.ec.maxDecimalExpansion, e.ec.maxStringExpansion, callee.Tok.Line)
			}
			result := i.evalEvalBuiltin(args, e)
			if hasResourceLimits {
				requireSafeSandboxValueLimits(lowName, []any{result}, e.ec.maxDecimalExpansion, e.ec.maxStringExpansion, callee.Tok.Line)
			}
			return result
		}
		if val, ok := e.get(fnName); ok {
			if bf, ok2 := val.(ReadOnlyBuiltinFunc); ok2 {
				result, err := bf(args, callee.Tok.File, callee.Tok.Line)
				if err != nil {
					panic(dslStop{err: err})
				}
				// The callback itself opted into read-only use, but its return value
				// may still be an object capability. Keep that value behind the same
				// membrane; otherwise a condition could feed a returned writer to a
				// mutating builtin such as ЗаполнитьЗначенияСвойств.
				return protectReadOnly(e.ec, result)
			}
			if bf, ok2 := val.(BuiltinFunc); ok2 {
				refuseReadOnly(e.ec, "вызов внешней функции «"+fnName+"»")
				result, err := bf(args, callee.Tok.File, callee.Tok.Line)
				if err != nil {
					panic(dslStop{err: err})
				}
				return result
			}
			if bf, ok2 := val.(FallbackBuiltinFunc); ok2 {
				refuseReadOnly(e.ec, "вызов внешней функции «"+fnName+"»")
				fallback = bf
			}
		}
		// Процедуры формы (.form.os) принадлежат текущему модулю и потому
		// разрешаются раньше любых глобальных экспортов. Они передаются через
		// vars["__form_procs__"] как map[lowercase]*ProcedureDecl.
		if fpAny, ok2 := e.rawFormProcs(); ok2 {
			if fp, ok3 := fpAny.(map[string]*ast.ProcedureDecl); ok3 {
				if proc, ok4 := fp[strings.ToLower(fnName)]; ok4 && sourceFile != "" && proc.Name.File == sourceFile {
					return i.callUserProc(proc, e, userArgs)
				}
			}
		}
		// СВОЁ РАНЬШЕ ЧУЖОГО. Помощник из того же файла (.proc.os /
		// .posting.os / .rep.os) ищется ПЕРЕД экспортом чужого модуля.
		//
		// Порядок был обратным, и любая экспортная функция полностью затеняла
		// одноимённую локальную: собственная функция модуля становилась
		// недостижимой, а неквалифицированный вызов молча уходил в чужой
		// экспорт. При несовпадении числа параметров недостающие вставали как
		// nil — то есть добавление нового модуля со вспомогательным именем
		// ломало посторонний, давно зелёный код, и ошибка указывала на строку в
		// НОВОМ файле (#717).
		//
		// Обращение к чужому экспорту остаётся квалифицированным: Модуль.Функция.
		//
		if i.LookupSiblingProc != nil && sourceFile != "" {
			if proc := i.LookupSiblingProc(sourceFile, fnName); proc != nil {
				return i.callUserProc(proc, e, userArgs)
			}
		}
		if i.LookupProc != nil {
			if proc := i.LookupProc(fnName); proc != nil {
				return i.callUserProc(proc, e, userArgs)
			}
		}
		if fallback != nil {
			result, err := fallback(args, callee.Tok.File, callee.Tok.Line)
			if err != nil {
				panic(dslStop{err: err})
			}
			return result
		}
		fn, ok := builtins[strings.ToLower(fnName)]
		if !ok {
			// Factory-вызов без Новый: ЧтениеТекста(Путь), Запрос(Текст), …
			if factory, ok2 := e.get("__factory_" + fnName); ok2 {
				if fn2, ok3 := factory.(func([]any) any); ok3 {
					refuseReadOnly(e.ec, "вызов внешней фабрики «"+fnName+"»")
					return fn2(args)
				}
			}
			panic(dslStop{err: fmt.Errorf("%s:%d: unknown function %q", callee.Tok.File, callee.Tok.Line, fnName)})
		}
		hasResourceLimits := e.ec != nil && (e.ec.maxDecimalExpansion > 0 || e.ec.maxStringExpansion > 0)
		// Только штатный builtin паузы получает deadline-aware dispatch. Это
		// закрывает провал к прямому ожиданию после одноимённой non-callable
		// переменной, но не ломает обычный порядок разрешения: пользовательская
		// процедура либо доверенная инъекция Sleep/Wait по-прежнему может
		// затенить builtin и сама контролируется общим дедлайном между операторами.
		if e.ec != nil && (!e.ec.deadline.IsZero() || (e.ec.context != nil && e.ec.context.Done() != nil)) && isSleepBuiltinName(lowName) {
			if hasResourceLimits {
				requireSafeSandboxBuiltinInputs(lowName, args, e.ec.maxDecimalExpansion, e.ec.maxStringExpansion, callee.Tok.Line)
			}
			waitForSleep(sleepDuration(args), e.ec)
			return nil
		}
		if hasResourceLimits {
			fn = boundedSandboxBuiltin(lowName, fn, e.ec.maxDecimalExpansion, e.ec.maxStringExpansion)
		}
		result, err := fn(args, callee.Tok.File, callee.Tok.Line)
		if err != nil {
			panic(dslStop{err: err})
		}
		return result
	case *ast.MemberExpr:
		recv := i.evalExpr(callee.Object, e)
		method := strings.ToLower(callee.Field.Literal)
		// Fail closed: receiver может быть writer'ом из локальной переменной,
		// а по одному имени невозможно надёжно отделить чтение от Записать/Write
		// и будущих алиасов. Поля объектов остаются доступны через MemberExpr.
		refuseReadOnly(e.ec, "вызов метода «"+callee.Field.Literal+"»")
		switch o := recv.(type) {
		case MethodCallable:
			if ml, ok := o.(MethodLister); ok {
				typeName, known := ml.KnownMethods()
				if !hasMethodFold(known, method) {
					unknownMethod(typeName, callee.Field.Literal, known)
				}
			}
			return o.CallMethod(method, args)
		case string:
			// Для ссылочных методов даём более предметную подсказку, чем общая
			// ошибка ниже. Это частая ловушка колонок «Ссылка» из запроса, но не
			// утверждаем, что произвольная строка обязательно получена оттуда.
			if refMethodOnString(method) {
				RaiseUserError(callee.Field.Literal + "() вызван у строки. Если это колонка «Ссылка» " +
					"из результата запроса, она содержит UUID, а не ссылку с менеджером. Получите ссылку через " +
					"Справочники.<Тип>.НайтиПоИдентификатору(Строка(Стр.Ссылка)) " +
					"(для документов — Документы.<Тип>.НайтиПоИдентификатору)")
			}
		}
		// Если object — идентификатор, не разрешившийся в значение,
		// и это известный модуль — резолвим Module.Proc() (
		if recv == nil && i.LookupModuleProc != nil {
			if objIdent, ok := callee.Object.(*ast.Ident); ok {
				if proc := i.LookupModuleProc(objIdent.Tok.Literal, callee.Field.Literal); proc != nil {
					return i.callUserProc(proc, e, userArgs)
				}
			}
		}
		if recv == nil {
			// Имя приёмника в тексте ошибки экономит поиск: «вызван у Неопределено»
			// без него не отвечает на главный вопрос — ЧТО именно пустое. Чаще
			// всего это ссылка ещё не записанного объекта.
			if src := exprSourceName(callee.Object); src != "" {
				RaiseUserError("Метод " + callee.Field.Literal + " вызван у Неопределено: «" + src + "» не заполнено")
			}
			RaiseUserError("Метод " + callee.Field.Literal + " вызван у Неопределено")
		}
		// Приёмник есть, но методов у его типа нет вовсе (строка, число, дата,
		// булево). Прежде такой вызов молча возвращал Неопределено, и опечатка в
		// имени метода — или просто неверный тип значения — превращалась в
		// бесшумную потерю функциональности: код «отрабатывал», ничего не сделав
		// (#718). Отказ должен быть слышен.
		RaiseUserError("Метод " + callee.Field.Literal + " не существует у значения типа " +
			getTypeName(recv) + " — у этого типа методов нет")
		return nil
	}
	return nil
}

// callSourceFile отделяет диагностическое имя динамического выражения от
// identity модуля, в области которого оно исполняется. Для обычного AST файл
// токена всегда авторитетен — это важно, например, для default-выражения
// процедуры B, вычисляемого в variable scope вызывающей процедуры A.
func callSourceFile(tokenFile string, e *env) string {
	switch tokenFile {
	case "<Вычислить>", "<console>":
		if e != nil {
			return e.sourceFile
		}
	}
	return tokenFile
}

// refMethodOnString отвечает, является ли метод «ссылочным» — таким, который
// имеет смысл только у Ref (Ссылка.ПолучитьОбъект() и соседи). Для остальных
// вызовов работает общая диагностика неизвестного метода из evalCall.
func refMethodOnString(method string) bool {
	switch method {
	case "получитьобъект", "getobject", "удалить", "delete", "записать", "write":
		return true
	}
	return false
}

// exprSourceName восстанавливает исходный текст простого выражения-приёмника
// («Ссылка», «Объект.Ссылка») для сообщений об ошибках. Для всего сложнее
// цепочки идентификаторов возвращает "" — тогда сообщение остаётся прежним.
func exprSourceName(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Tok.Literal
	case *ast.MemberExpr:
		if base := exprSourceName(v.Object); base != "" {
			return base + "." + v.Field.Literal
		}
	}
	return ""
}

// evalEvalBuiltin реализует Вычислить(Выражение): args[0] — строка-выражение.
// Разбирается через parser.ParseExpr и вычисляется в переданном окружении,
// поэтому выражение видит локальные переменные процедуры.
func (i *Interpreter) evalEvalBuiltin(args []any, e *env) any {
	if len(args) == 0 {
		return nil
	}
	src, ok := args[0].(string)
	if !ok {
		panic(userError{Msg: "Вычислить: ожидается строка-выражение"})
	}
	if e != nil && e.ec != nil && (e.ec.maxDecimalExpansion > 0 || e.ec.maxStringExpansion > 0 || e.ec.readOnlyReason != "") {
		if err := ValidateUntrustedExpressionSource(src); err != nil {
			panic(userError{Msg: "Вычислить: " + err.Error()})
		}
	}
	limit := i.MaxEvalDepth
	if limit <= 0 {
		limit = defaultMaxEvalDepth
	}
	if e.ec.evalDepth >= limit {
		RaiseUserError(fmt.Sprintf("Превышена максимальная глубина Вычислить (%d) — вероятно, бесконечное динамическое выражение", limit))
	}
	e.ec.evalDepth++
	defer func() { e.ec.evalDepth-- }()

	// Диагностическое имя остаётся синтетическим: строка выражения действительно
	// начинается с line 1, но это не line 1 физического модуля. Лексическая
	// identity для sibling-поиска хранится отдельно в e.sourceFile.
	p := parser.New(lexer.New(src, "<Вычислить>"))
	expr, err := p.ParseStandaloneExpr()
	if err != nil {
		panic(userError{Msg: "Вычислить: " + err.Error()})
	}
	return i.evalExpr(expr, e)
}

func (i *Interpreter) callUserProc(proc *ast.ProcedureDecl, callEnv *env, args []any) (retVal any) {
	return i.callUserProcAtDepth(proc, callEnv, args, callEnv.depth+1)
}

func (i *Interpreter) callEntryProc(proc *ast.ProcedureDecl, root *env, args []any) (retVal any) {
	return i.callUserProcAtDepth(proc, root, args, root.depth)
}

func (i *Interpreter) moduleEnvFor(proc *ast.ProcedureDecl, root *env) *env {
	if proc == nil || root == nil || len(proc.ModuleVars) == 0 {
		return nil
	}
	key := proc.ModuleKey
	if key == "" {
		key = proc.Name.File
	}
	if key == "" {
		return nil
	}
	if root.ec.moduleEnvs == nil {
		root.ec.moduleEnvs = make(map[string]*env)
	}
	if me := root.ec.moduleEnvs[key]; me != nil {
		return me
	}
	names := make(map[string]bool)
	vars := make(map[string]any)
	for _, decl := range proc.ModuleVars {
		for _, tok := range decl.Names {
			name := strings.ToLower(tok.Literal)
			if name == "" {
				continue
			}
			names[name] = true
			vars[name] = nil
		}
	}
	if len(names) == 0 {
		return nil
	}
	me := root.frameWithModule(root, nil, root.depth)
	me.vars = vars
	me.moduleVars = names
	root.ec.moduleEnvs[key] = me
	return me
}

func (i *Interpreter) callUserProcAtDepth(proc *ast.ProcedureDecl, callEnv *env, args []any, frameDepth int) (retVal any) {
	// Страж рекурсии: env нового кадра будет на уровень глубже вызывающего.
	// Обрываем ДО создания кадра и проброса в отладчик, иначе бесконечная
	// рекурсия переполнит стек горутины и аварийно уронит процесс (мимо Попытки).
	limit := i.MaxRecursionDepth
	if limit <= 0 {
		limit = defaultMaxRecursionDepth
	}
	if frameDepth > limit {
		RaiseUserError(fmt.Sprintf("Превышена максимальная глубина рекурсии (%d) — вероятно, бесконечный вызов процедуры/функции", limit))
	}
	if hook := callEnv.ec.debug; hook != nil {
		hook.HookPushFrame(proc.Name.Literal, 0)
		defer hook.HookPopFrame()
	}
	defer func() {
		if r := recover(); r != nil {
			switch s := r.(type) {
			case dslReturn:
				retVal = s.val
			default:
				panic(r)
			}
		}
	}()
	parentEnv := callEnv
	defaultEnv := callEnv
	moduleEnv := i.moduleEnvFor(proc, callEnv.rootEnv())
	if i.StrictLexicalScope {
		parentEnv = callEnv.rootEnv()
		if moduleEnv != nil {
			parentEnv = moduleEnv
		}
		defaultEnv = parentEnv
	} else if moduleEnv != nil {
		defaultEnv = callEnv.frameWithModule(callEnv, moduleEnv, callEnv.depth)
	}
	child := callEnv.frameWithModule(parentEnv, moduleEnv, frameDepth)
	child.sourceFile = proc.Name.File
	for idx, param := range proc.Params {
		if idx < len(args) {
			if _, missing := args[idx].(missingNamedArg); !missing {
				child.setLocal(param.Literal, args[idx])
				continue
			}
		}
		// Параметр без переданного значения — пробуем дефолт. В legacy
		// дефолт вычисляется в callEnv; в strict lexical — в module-env/root,
		// чтобы не оставлять обходной доступ к локальным переменным caller-а.
		// child ещё не имеет других параметров — сознательно не даём дефолтам
		// ссылаться на «соседей» (1С-семантика).
		if idx < len(proc.Defaults) && proc.Defaults[idx] != nil {
			// Значения переменных берём из прежнего defaultEnv, но lexical
			// identity принадлежит AST вызываемой процедуры. Shallow-copy не
			// копирует карты/цепочку scope и потому сохраняет legacy visibility.
			exprEnv := *defaultEnv
			exprEnv.sourceFile = proc.Name.File
			child.setLocal(param.Literal, i.evalExpr(proc.Defaults[idx], &exprEnv))
		} else {
			child.setLocal(param.Literal, nil)
		}
	}
	i.execBlock(proc.Body, child)
	return nil
}

// evalArgs — аргументы для всех, кроме пользовательской процедуры: пропуск
// («Метод(А,,Б)») здесь уже нормализован в nil, то есть Неопределено.
func (i *Interpreter) evalArgs(exprs []ast.Expr, e *env) []any {
	return clearMissingArgs(i.evalArgsKeepMissing(exprs, e))
}

// evalArgsKeepMissing вычисляет аргументы, сохраняя пропуски как внутренний
// sentinel missingNamedArg. Такой список принимает ТОЛЬКО callUserProc: он
// отличает «значение не передали» от «передали Неопределено» и в первом случае
// вычисляет Defaults[idx] — ровно то различие, ради которого пропуск и пишут
// (issue #1160). Механизм не новый: тем же sentinel помечает дыры BindNamedArgs.
func (i *Interpreter) evalArgsKeepMissing(exprs []ast.Expr, e *env) []any {
	args := make([]any, len(exprs))
	for idx, a := range exprs {
		if _, skipped := a.(*ast.MissingArg); skipped {
			args[idx] = missingNamedArg{}
			continue
		}
		args[idx] = i.evalExpr(a, e)
	}
	return args
}

// clearMissingArgs заменяет sentinel пропуска на nil. Инвариант: за пределы
// callUserProc sentinel не выходит — встроенная функция, метод объекта или
// фабрика увидели бы вместо Неопределено внутренний тип интерпретатора и
// свалились бы на нём непонятной ошибкой. Копия делается только когда пропуск
// действительно есть: обычный вызов идёт прежним путём, без лишней аллокации,
// и без риска, что нормализация отзеркалится в список для callUserProc.
func clearMissingArgs(args []any) []any {
	skipped := false
	for _, a := range args {
		if _, ok := a.(missingNamedArg); ok {
			skipped = true
			break
		}
	}
	if !skipped {
		return args
	}
	out := make([]any, len(args))
	copy(out, args)
	for idx, a := range out {
		if _, ok := a.(missingNamedArg); ok {
			out[idx] = nil
		}
	}
	return out
}

func truthy(v any) bool {
	if v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t != ""
	}
	// Числовой ноль ложен в любом Go-типе. Раньше здесь стояли только float64 и
	// decimal, а целые проваливались в «всё остальное — истина»: булево поле из
	// запроса на SQLite приходит как int64, поэтому `Если Стр.Флаг Тогда` для
	// Ложь молча выбирал ветку «истина» (issue #704).
	if zero, ok := numericZero(v); ok {
		return !zero
	}
	return true
}

// equalOperator и compareOperator — путь операторов «=», «<>», «<», «>», «<=»,
// «>=». Подстановка нуля вместо незаполненного числа (#1136) живёт здесь, а не
// внутри equalSandboxed/compareSandboxed, и это граница правки, а не деталь
// оформления: тот же comparator обслуживает поиск и сортировку коллекций
// (compareAny → Массив.Найти/Сортировать, ТаблицаЗначений.Найти/НайтиСтроки/
// Сортировать). Просочись правило туда — ТЗ.НайтиСтроки(Новый Структура("Цена",
// 0)) начал бы возвращать незаполненные строки, а отбор по Неопределено —
// заполненные нули, то есть оба естественных способа разделить «пусто» и «ноль»
// перестали бы работать разом. Соответствие при этом ищет своим путём (refKey) и
// на подстановку не реагирует вовсе, так что платформа разошлась бы сама с собой:
// Соответствие.Получить(0) пустой ключ не находит, а ТЗ.НайтиСтроки — находил бы.
func equalOperator(a, b any, ec *execCtx, line int) bool {
	a, b = nilAsNumericZero(a, b)
	return equalSandboxed(a, b, ec, line)
}

func compareOperator(a, b any, ec *execCtx, line int) int {
	a, b = nilAsNumericZero(a, b)
	return compareSandboxed(a, b, ec, line)
}

// equal — равенство с семантикой оператора «=». Единственный вызывающий —
// утверждения (Утверждать.Равно/НеРавно, МаскаПоля): проверка конфигурации
// обязана видеть ровно то, что увидит модуль, иначе `Утверждать.Равно(Стр.Цена, 0)`
// краснел бы там, где `Если Стр.Цена = 0` выбирает ветку. Поиск по коллекциям
// сюда не ходит — он идёт через compare и refKey.
func equal(a, b any) bool {
	return equalOperator(a, b, nil, 0)
}

func equalSandboxed(a, b any, ec *execCtx, line int) bool {
	// Даты сравниваем хронологически — тем же правилом, что и «<»/«>» ниже.
	// Через refKey один и тот же момент в разных зонах давал разные ключи:
	// дата из запроса приходит в UTC (на SQLite она хранится строкой RFC3339),
	// а литерал Дата(...) строится в локальной зоне. Получалось «меньше либо
	// равно истинно, больше либо равно истинно, а равно ложно» — отбор
	// «Если Стр.Дата = ДатаДокумента» молча не находил совпадений (#1034).
	if at, ok := a.(time.Time); ok {
		if bt, ok2 := b.(time.Time); ok2 {
			return at.Equal(bt)
		}
	}
	// Числа сравниваем по значению (decimal.Equal), а не строково: иначе
	// decimal(5) и int64(5) или 0.10 и 0.1 могли бы разойтись. Строки/ссылки —
	// по-прежнему через refKey.
	if isNumeric(a) && isNumeric(b) {
		ad, aok := toDecimal(a)
		bd, bok := toDecimal(b)
		if aok && bok {
			requireSafeSandboxDecimalOperation(ec, "=", ad, line)
			requireSafeSandboxDecimalOperation(ec, "=", bd, line)
			return ad.Equal(bd)
		}
	}
	if ec != nil && ec.maxStringExpansion > 0 {
		requireSafeSandboxFormattingValueLimits("=", []any{a, b}, ec.maxDecimalExpansion, ec.maxStringExpansion, line)
	}
	return refKey(a) == refKey(b)
}

// dateAddSeconds сдвигает дату на sec секунд (семантика арифметики дат 1С).
func dateAddSeconds(t time.Time, sec float64) time.Time {
	const maxWholeSeconds = float64(math.MaxInt64 / int64(time.Second))
	if math.IsNaN(sec) || math.IsInf(sec, 0) || sec > maxWholeSeconds || sec < -maxWholeSeconds {
		RaiseUserError("сдвиг даты выходит за безопасный диапазон времени")
	}
	return safeDateResult(t.Add(time.Duration(sec * float64(time.Second))))
}

// compare — порядок БЕЗ подстановки нуля вместо незаполненного числа. Отсюда
// работает compareAny, а через него поиск и сортировка коллекций: «нет значения»
// остаётся значением, отличным от нуля, как и до #1136. Операторы ходят не сюда,
// а через compareOperator.
func compare(a, b any) int {
	return compareSandboxed(a, b, nil, 0)
}

func compareSandboxed(a, b any, ec *execCtx, line int) int {
	// Даты сравниваем хронологически, а не как строки.
	if at, ok := a.(time.Time); ok {
		if bt, ok2 := b.(time.Time); ok2 {
			switch {
			case at.Before(bt):
				return -1
			case at.After(bt):
				return 1
			default:
				return 0
			}
		}
	}
	requireSafeSandboxDecimalOperand(ec, "comparison", a, line)
	requireSafeSandboxDecimalOperand(ec, "comparison", b, line)
	ad, aok := toDecimal(a)
	bd, bok := toDecimal(b)
	if aok && bok {
		requireSafeSandboxDecimalOperation(ec, "comparison", ad, line)
		requireSafeSandboxDecimalOperation(ec, "comparison", bd, line)
		return ad.Cmp(bd)
	}
	if ec != nil && ec.maxStringExpansion > 0 {
		requireSafeSandboxFormattingValueLimits("comparison", []any{a, b}, ec.maxDecimalExpansion, ec.maxStringExpansion, line)
	}
	as := fmt.Sprintf("%v", a)
	bs := fmt.Sprintf("%v", b)
	if as < bs {
		return -1
	}
	if as > bs {
		return 1
	}
	return 0
}

func toFloatOr0(v any) float64 {
	f, _ := toFloat(v)
	return f
}

// execTry выполняет Попытка/Исключение.
// Только userError перехватывается; системные паники и dslReturn пробрасываются дальше.
func (i *Interpreter) execTry(t *ast.TryStmt, e *env) {
	var caught *userError
	func() {
		defer func() {
			if r := recover(); r != nil {
				if ue, ok := r.(userError); ok {
					caught = &ue
					return
				}
				panic(r) // dslReturn, dslStop, loopBreak, loopContinue — пробрасываем
			}
		}()
		i.execBlock(t.Try, e)
	}()
	if caught != nil {
		if len(t.Except) == 0 {
			// Нет блока Исключение — пробрасываем ошибку дальше
			panic(*caught)
		}
		msg := caught.Msg
		descFn := BuiltinFunc(func(args []any, file string, line int) (any, error) {
			return msg, nil
		})
		// ИнформацияОбОшибке() → Структура с полями Описание/НомерСтроки/Источник.
		// Возвращаем *Struct (а не отдельный тип), чтобы Инфо.Описание работало
		// через существующую ветку MemberExpr без правок диспетчера.
		errInfo := newErrorInfo(caught)
		infoFn := BuiltinFunc(func(args []any, file string, line int) (any, error) {
			return errInfo, nil
		})
		rethrowFn := BuiltinFunc(func(args []any, file string, line int) (any, error) {
			if len(args) == 0 {
				panic(*caught)
			}
			return raiseUserException(args, file, line)
		})
		// ОписаниеОшибки/ИнформацияОбОшибке доступны только внутри блока
		// Исключение, поэтому публикуются временно. Сам блок исполняется в
		// текущем scope (не в child) — чтобы переменные, впервые присвоенные в
		// Исключение, были видны после КонецПопытки, как в 1С (см. П.39).
		restore := publishTemp(e, map[string]any{
			"ОписаниеОшибки":     descFn,
			"ErrorDescription":   descFn,
			"ИнформацияОбОшибке": infoFn,
			"ErrorInfo":          infoFn,
			"ВызватьИсключение":  rethrowFn,
			"Raise":              rethrowFn,
		})
		func() {
			defer restore()
			i.execBlock(t.Except, e)
		}()
	}
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return 0, false
		}
		return t, true
	case float32:
		f := float64(t)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	case decimal.Decimal:
		return decimalToFiniteFloat64(t)
	case *decimal.Decimal:
		if t == nil {
			return 0, false
		}
		return decimalToFiniteFloat64(*t)
	case int:
		return float64(t), true
	case int8:
		return float64(t), true
	case int16:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint:
		return float64(t), true
	case uint8:
		return float64(t), true
	case uint16:
		return float64(t), true
	case uint32:
		return float64(t), true
	case uint64:
		return float64(t), true
	case string:
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return 0, false
			}
			return f, true
		}
	}
	return 0, false
}

// applyCompoundOp computes the result of a compound assignment operator.
func applyCompoundOp(op token.Type, old, val any, ec *execCtx) any {
	if ec != nil {
		requireSafeSandboxValueLimits("compound assignment", []any{old, val}, ec.maxDecimalExpansion, ec.maxStringExpansion, 0)
	}
	requireSafeSandboxDecimalOperand(ec, "compound assignment", old, 0)
	requireSafeSandboxDecimalOperand(ec, "compound assignment", val, 0)
	ld, lok := toDecimal(old)
	rd, rok := toDecimal(val)
	if lok && rok {
		requireSafeSandboxDecimalOperation(ec, "compound assignment", ld, 0)
		requireSafeSandboxDecimalOperation(ec, "compound assignment", rd, 0)
		switch op {
		case token.PLUS_ASSIGN:
			return safeSandboxDecimalResult(ec, "compound assignment", ld.Add(rd), 0)
		case token.MINUS_ASSIGN:
			return safeSandboxDecimalResult(ec, "compound assignment", ld.Sub(rd), 0)
		case token.STAR_ASSIGN:
			return safeSandboxDecimalResult(ec, "compound assignment", ld.Mul(rd), 0)
		case token.SLASH_ASSIGN:
			if !rd.IsZero() {
				requireSafeDecimalQuotient(ld, rd, 0)
				return safeSandboxDecimalResult(ec, "compound assignment", ld.Div(rd), 0)
			}
			return decimal.Zero
		}
	}
	if op == token.PLUS_ASSIGN {
		return sandboxConcatValues(ec, "compound assignment", old, val, 0)
	}
	return val
}
