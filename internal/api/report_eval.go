package api

import (
	"errors"
	"fmt"
	"sync"

	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/report/compose"
)

type reportEvaluator struct {
	interp *interpreter.Interpreter
	mu     sync.Mutex
	cache  map[string]*ast.ProcedureDecl
}

var _ compose.Evaluator = (*reportEvaluator)(nil)

func newReportEvaluator(interp *interpreter.Interpreter) *reportEvaluator {
	return &reportEvaluator{interp: interp, cache: map[string]*ast.ProcedureDecl{}}
}

func (e *reportEvaluator) compile(expr string) (*ast.ProcedureDecl, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p, ok := e.cache[expr]; ok {
		return p, nil
	}
	src := "Функция __cond()\nВозврат (" + expr + ");\nКонецФункции\n"
	prog, err := parser.New(lexer.New(src, "cond.os")).ParseProgram()
	if err != nil {
		return nil, err
	}
	var proc *ast.ProcedureDecl
	for _, d := range prog.Procedures {
		proc = d
		break
	}
	if proc == nil {
		return nil, fmt.Errorf("пустое выражение условия")
	}
	e.cache[expr] = proc
	return proc, nil
}

func (e *reportEvaluator) EvalBool(expr string, row compose.Row) (bool, error) {
	proc, err := e.compile(expr)
	if err != nil {
		return false, err
	}
	var result any
	if err := e.interp.RunWithResult(proc, &interpreter.MapThis{M: row}, &result, map[string]any(row)); err != nil {
		if errors.Is(err, interpreter.ErrDivisionByZero) {
			return false, nil
		}
		return false, err
	}
	b, _ := result.(bool)
	return b, nil
}

func (e *reportEvaluator) EvalNum(expr string, row compose.Row) (decimal.Decimal, bool, error) {
	proc, err := e.compile(expr)
	if err != nil {
		return decimal.Zero, false, err
	}
	var result any
	if err := e.interp.RunWithResult(proc, &interpreter.MapThis{M: row}, &result, map[string]any(row)); err != nil {
		if errors.Is(err, interpreter.ErrDivisionByZero) {
			return decimal.Zero, false, nil
		}
		return decimal.Zero, false, err
	}
	d, ok := compose.ExportToDecimal(result)
	return d, ok, nil
}
