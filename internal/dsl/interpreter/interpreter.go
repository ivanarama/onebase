package interpreter

import (
	"fmt"
	"strconv"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/token"
)

// sentinel to unwind call stack on Error()
type dslStop struct{ err error }

// sentinel to unwind call stack on Return
type dslReturn struct{ val any }


type Interpreter struct{}

func New() *Interpreter { return &Interpreter{} }

// Run executes a procedure. Optional extra vars (e.g. {"Движения": collector}) are
// injected into the top-level environment.
func (i *Interpreter) Run(proc *ast.ProcedureDecl, this This, extraVars ...map[string]any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			switch s := r.(type) {
			case dslStop:
				err = s.err
			case dslReturn:
				// early return from procedure — not an error
			default:
				panic(r)
			}
		}
	}()
	e := newEnv(this)
	for _, m := range extraVars {
		for k, v := range m {
			e.set(k, v)
		}
	}
	i.execBlock(proc.Body, e)
	return nil
}

func (i *Interpreter) execBlock(stmts []ast.Stmt, e *env) {
	for _, s := range stmts {
		i.execStmt(s, e)
	}
}

func (i *Interpreter) execStmt(s ast.Stmt, e *env) {
	switch v := s.(type) {
	case *ast.IfStmt:
		cond := i.evalExpr(v.Cond, e)
		if truthy(cond) {
			i.execBlock(v.Then, e.child())
		} else if len(v.Else) > 0 {
			i.execBlock(v.Else, e.child())
		}
	case *ast.ForEachStmt:
		coll := i.evalExpr(v.Collection, e)
		switch items := coll.(type) {
		case []map[string]any:
			for _, row := range items {
				child := e.child()
				child.set(v.Var.Literal, &MapThis{M: row})
				i.execBlock(v.Body, child)
			}
		case []any:
			for _, item := range items {
				child := e.child()
				child.set(v.Var.Literal, item)
				i.execBlock(v.Body, child)
			}
		}
	case *ast.AssignStmt:
		val := i.evalExpr(v.Value, e)
		i.assign(v.Target, val, e)
	case *ast.ExprStmt:
		i.evalExpr(v.X, e)
	case *ast.VarDecl:
		e.set(v.Name.Literal, nil)
	case *ast.NumericForStmt:
		start := toFloatOr0(i.evalExpr(v.Start, e))
		end := toFloatOr0(i.evalExpr(v.End, e))
		for counter := start; counter <= end; counter++ {
			child := e.child()
			child.set(v.Var.Literal, counter)
			i.execBlock(v.Body, child)
		}
	case *ast.ReturnStmt:
		var val any
		if v.Value != nil {
			val = i.evalExpr(v.Value, e)
		}
		panic(dslReturn{val: val})
	}
}

func (i *Interpreter) assign(target ast.Expr, val any, e *env) {
	switch t := target.(type) {
	case *ast.Ident:
		e.set(t.Tok.Literal, val)
	case *ast.MemberExpr:
		obj := i.evalExpr(t.Object, e)
		if th, ok := obj.(This); ok {
			th.Set(t.Field.Literal, val)
		}
	}
}

func (i *Interpreter) evalExpr(expr ast.Expr, e *env) any {
	switch v := expr.(type) {
	case *ast.StringLit:
		return v.Value
	case *ast.NumberLit:
		f, _ := strconv.ParseFloat(v.Value, 64)
		return f
	case *ast.Ident:
		val, _ := e.get(v.Tok.Literal)
		return val
	case *ast.MemberExpr:
		obj := i.evalExpr(v.Object, e)
		if th, ok := obj.(This); ok {
			return th.Get(v.Field.Literal)
		}
		return nil
	case *ast.BinaryExpr:
		return i.evalBinary(v, e)
	case *ast.CallExpr:
		return i.evalCall(v, e)
	}
	return nil
}

func (i *Interpreter) evalBinary(b *ast.BinaryExpr, e *env) any {
	l := i.evalExpr(b.Left, e)
	r := i.evalExpr(b.Right, e)
	switch b.Op.Type {
	case token.ASSIGN: // equality in conditions
		return equal(l, r)
	case token.NEQ:
		return !equal(l, r)
	case token.LT:
		return compare(l, r) < 0
	case token.GT:
		return compare(l, r) > 0
	case token.LTE:
		return compare(l, r) <= 0
	case token.GTE:
		return compare(l, r) >= 0
	case token.PLUS:
		lf, lok := toFloat(l)
		rf, rok := toFloat(r)
		if lok && rok {
			return lf + rf
		}
		return fmt.Sprintf("%v", l) + fmt.Sprintf("%v", r)
	case token.MINUS:
		lf, lok := toFloat(l)
		rf, rok := toFloat(r)
		if lok && rok {
			return lf - rf
		}
	case token.STAR:
		lf, lok := toFloat(l)
		rf, rok := toFloat(r)
		if lok && rok {
			return lf * rf
		}
	case token.SLASH:
		lf, lok := toFloat(l)
		rf, rok := toFloat(r)
		if lok && rok && rf != 0 {
			return lf / rf
		}
	}
	return nil
}

func (i *Interpreter) evalCall(c *ast.CallExpr, e *env) any {
	args := i.evalArgs(c.Args, e)
	switch callee := c.Callee.(type) {
	case *ast.Ident:
		fn, ok := builtins[callee.Tok.Literal]
		if !ok {
			panic(dslStop{err: fmt.Errorf("%s:%d: unknown function %q", callee.Tok.File, callee.Tok.Line, callee.Tok.Literal)})
		}
		result, err := fn(args, callee.Tok.File, callee.Tok.Line)
		if err != nil {
			panic(dslStop{err: err})
		}
		return result
	case *ast.MemberExpr:
		recv := i.evalExpr(callee.Object, e)
		if mc, ok := recv.(MethodCallable); ok {
			return mc.CallMethod(callee.Field.Literal, args)
		}
		return nil
	}
	return nil
}

func (i *Interpreter) evalArgs(exprs []ast.Expr, e *env) []any {
	args := make([]any, len(exprs))
	for idx, a := range exprs {
		args[idx] = i.evalExpr(a, e)
	}
	return args
}

func truthy(v any) bool {
	if v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t != ""
	}
	return true
}

func equal(a, b any) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func compare(a, b any) int {
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	if aok && bok {
		if af < bf {
			return -1
		}
		if af > bf {
			return 1
		}
		return 0
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

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}
