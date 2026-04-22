package interpreter

import (
	"fmt"
	"strconv"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/token"
)

// sentinel to unwind call stack on Error()
type dslStop struct{ err error }

type Interpreter struct{}

func New() *Interpreter { return &Interpreter{} }

func (i *Interpreter) Run(proc *ast.ProcedureDecl, this This) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if s, ok := r.(dslStop); ok {
				err = s.err
			} else {
				panic(r)
			}
		}
	}()
	e := newEnv(this)
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
	case *ast.AssignStmt:
		val := i.evalExpr(v.Value, e)
		i.assign(v.Target, val, e)
	case *ast.ExprStmt:
		i.evalExpr(v.X, e)
	case *ast.VarDecl:
		e.set(v.Name.Literal, nil)
	}
}

func (i *Interpreter) assign(target ast.Expr, val any, e *env) {
	switch t := target.(type) {
	case *ast.Ident:
		e.set(t.Tok.Literal, val)
	case *ast.MemberExpr:
		if id, ok := t.Object.(*ast.Ident); ok && id.Tok.Literal == "this" {
			if th, ok := e.this.(This); ok {
				th.Set(t.Field.Literal, val)
			}
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
	case token.ASSIGN: // used as equality in conditions
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
	}
	return nil
}

func (i *Interpreter) evalCall(c *ast.CallExpr, e *env) any {
	fn, ok := builtins[c.Callee.Literal]
	if !ok {
		panic(dslStop{err: fmt.Errorf("%s:%d: unknown function %q", c.Callee.File, c.Callee.Line, c.Callee.Literal)})
	}
	args := make([]any, len(c.Args))
	for idx, a := range c.Args {
		args[idx] = i.evalExpr(a, e)
	}
	result, err := fn(args, c.Callee.File, c.Callee.Line)
	if err != nil {
		panic(dslStop{err: err})
	}
	return result
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

func toFloat(v any) (float64, bool) {
	if f, ok := v.(float64); ok {
		return f, true
	}
	return 0, false
}
