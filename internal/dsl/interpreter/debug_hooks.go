package interpreter

import (
	"fmt"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/shopspring/decimal"
)

// ── Source location extraction from AST ──────────────────────────

// sourceLocation for internal use (maps to debugger.Location)
type sourceLocation struct {
	File string
	Line int
	Col  int
}

// getLocation extracts source location from an AST statement
func getLocation(stmt ast.Stmt) *sourceLocation {
	var file string
	var line, col int

	switch s := stmt.(type) {
	case *ast.IfStmt:
		file, line, col = getExprLocation(s.Cond)
	case *ast.ForEachStmt:
		file, line, col = s.Var.File, s.Var.Line, s.Var.Col
	case *ast.NumericForStmt:
		file, line, col = s.Var.File, s.Var.Line, s.Var.Col
	case *ast.WhileStmt:
		file, line, col = s.Tok.File, s.Tok.Line, s.Tok.Col
	case *ast.AssignStmt:
		file, line, col = getExprLocation(s.Target)
	case *ast.ExprStmt:
		file, line, col = getExprLocation(s.X)
	case *ast.ReturnStmt:
		file, line, col = s.Tok.File, s.Tok.Line, s.Tok.Col
	case *ast.TryStmt:
		file, line, col = s.Tok.File, s.Tok.Line, s.Tok.Col
	case *ast.VarDecl:
		if len(s.Names) > 0 {
			file, line, col = s.Names[0].File, s.Names[0].Line, s.Names[0].Col
		}
	case *ast.BreakStmt:
		file, line, col = s.Tok.File, s.Tok.Line, s.Tok.Col
	case *ast.ContinueStmt:
		file, line, col = s.Tok.File, s.Tok.Line, s.Tok.Col
	default:
		return nil
	}

	if file == "" {
		return nil
	}
	return &sourceLocation{File: file, Line: line, Col: col}
}

// getExprLocation extracts source location from an AST expression.
// Returns the leftmost token position for accurate line tracking.
func getExprLocation(expr ast.Expr) (string, int, int) {
	if expr == nil {
		return "", 0, 0
	}
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Tok.File, e.Tok.Line, e.Tok.Col
	case *ast.StringLit:
		return e.Tok.File, e.Tok.Line, e.Tok.Col
	case *ast.NumberLit:
		return e.Tok.File, e.Tok.Line, e.Tok.Col
	case *ast.BoolLit:
		return e.Tok.File, e.Tok.Line, e.Tok.Col
	case *ast.MemberExpr:
		return e.Field.File, e.Field.Line, e.Field.Col
	case *ast.CallExpr:
		return getExprLocation(e.Callee)
	case *ast.BinaryExpr:
		return e.Op.File, e.Op.Line, e.Op.Col
	case *ast.UnaryExpr:
		return e.Op.File, e.Op.Line, e.Op.Col
	case *ast.NewExpr:
		return e.TypeName.File, e.TypeName.Line, e.TypeName.Col
	case *ast.IndexExpr:
		return getExprLocation(e.Object)
	case *ast.ArrayLit:
		return e.Tok.File, e.Tok.Line, e.Tok.Col
	default:
		return "", 0, 0
	}
}

// ── env helpers for debugger ─────────────────────────────────────

// GetLocals returns variables in the current scope only
func (e *env) GetLocals() map[string]any {
	result := make(map[string]any)
	for k, v := range e.vars {
		result[k] = v
	}
	return result
}

// GetAllVariables returns all visible variables (current + parent scopes)
func (e *env) GetAllVariables() map[string]any {
	result := make(map[string]any)
	current := e
	seenModule := false
	for current != nil {
		for k, v := range current.vars {
			if _, exists := result[k]; !exists {
				result[k] = v
			}
		}
		if !seenModule && current.module != nil {
			for k, v := range current.module.vars {
				if _, exists := result[k]; !exists {
					result[k] = v
				}
			}
			seenModule = true
		}
		current = current.parent
	}
	return result
}

// ── Type formatting helpers ──────────────────────────────────────

// getTypeName returns the DSL type name for a value
func getTypeName(v any) string {
	v = unwrapReadOnly(v)
	if v == nil {
		return "Неопределено"
	}
	switch v.(type) {
	case bool:
		return "Булево"
	case float64:
		return "Число"
	case decimal.Decimal:
		return "Число"
	case int, int32, int64:
		return "Число"
	case string:
		return "Строка"
	case time.Time:
		return "Дата"
	case *Array:
		return "Массив"
	case *Map:
		return "Соответствие"
	case *Struct:
		return "Структура"
	default:
		return fmt.Sprintf("%T", v)
	}
}
