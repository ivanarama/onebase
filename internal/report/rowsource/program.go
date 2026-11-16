// Package rowsource runs a bounded, readonly row enrichment with host provenance.
// It is a source kernel; HTTP/preset/snapshot adapters must retain its descriptors.
package rowsource

import (
	"fmt"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/shopspring/decimal"
	"sort"
	"strings"
)

// The allowlist is intentionally smaller than the full DSL. Adding a builtin,
// constructor, method or AST node requires a new purity/capability review.
var pureFunctions = words("строка str сокрлп trimall врег upper нрег lower лев left прав right сред mid стрдлина strlen стрнайти strfind стрзаменить strreplace стрсоединить strjoin стрразделить strsplit символ chr char пустаястрока isblankstring значениезаполнено isfilled типзнч typeof")
var dataMethods = words("добавить add получить get установить set количество count вставить insert свойство property вграница upperbound выгрузить unload установитьпараметр setparameter выполнить execute уникальныйидентификатор uuid")
var dataConstructors = words("массив array структура struct соответствие map запрос query")

func words(text string) map[string]bool {
	out := map[string]bool{}
	for _, s := range strings.Fields(text) {
		out[s] = true
	}
	return out
}

type program struct {
	procedures map[string]*ast.ProcedureDecl
	entry      *ast.ProcedureDecl
	operations map[string]bool
	evidence   *programEvidence
}

func compileProgram(code, entry string) (*program, error) {
	// Iterative token/depth bounds run before the recursive parser.
	if err := interpreter.ValidateUntrustedExpressionSource(code); err != nil {
		return nil, err
	}
	tree, err := parser.New(lexer.New(code, "<report-row-source>")).ParseProgram()
	if err != nil {
		return nil, fmt.Errorf("row source: %w", err)
	}
	if len(tree.Body) > 0 {
		return nil, fmt.Errorf("row source: исполняемое тело модуля запрещено")
	}
	evidence, err := newProgramEvidence(code)
	if err != nil {
		return nil, err
	}
	p := &program{procedures: map[string]*ast.ProcedureDecl{}, operations: map[string]bool{}, evidence: evidence}
	for _, proc := range tree.Procedures {
		name := strings.ToLower(proc.Name.Literal)
		if p.procedures[name] != nil {
			return nil, fmt.Errorf("row source: повторная процедура")
		}
		p.procedures[name] = proc
	}
	p.entry = p.procedures[strings.ToLower(entry)]
	if p.entry == nil || !p.entry.Export || len(p.entry.Params) != 0 {
		return nil, fmt.Errorf("row source: нужна экспортная функция без параметров")
	}
	nodes := 0
	for _, proc := range tree.Procedures {
		for _, value := range proc.Defaults {
			if value != nil {
				if err := p.expr(value, &nodes, 0); err != nil {
					return nil, err
				}
			}
		}
		if err := p.block(proc.Body, &nodes, 0); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func sourceBudget(nodes *int, depth int) error {
	*nodes++
	if *nodes > 512 || depth > 64 {
		return fmt.Errorf("row source: превышен предел сложности")
	}
	return nil
}

func (p *program) block(body []ast.Stmt, nodes *int, depth int) error {
	expr := func(e ast.Expr) error { return p.expr(e, nodes, depth+1) }
	block := func(b []ast.Stmt) error { return p.block(b, nodes, depth+1) }
	for _, stmt := range body {
		if err := sourceBudget(nodes, depth); err != nil {
			return err
		}
		statementOrdinal := *nodes
		var err error
		switch v := stmt.(type) {
		case *ast.VarDecl, *ast.BreakStmt, *ast.ContinueStmt:
		case *ast.AssignStmt:
			if err = expr(v.Target); err == nil {
				err = expr(v.Value)
			}
			if err == nil && p.evidence != nil {
				p.evidence.recordTextSite(v, statementOrdinal)
			}
		case *ast.ExprStmt:
			err = expr(v.X)
		case *ast.ReturnStmt:
			if v.Value != nil {
				err = expr(v.Value)
			}
		case *ast.ForEachStmt:
			if err = expr(v.Collection); err == nil {
				err = block(v.Body)
			}
		case *ast.NumericForStmt:
			if err = expr(v.Start); err == nil {
				err = expr(v.End)
			}
			if err == nil {
				err = block(v.Body)
			}
		case *ast.IfStmt:
			if err = expr(v.Cond); err == nil {
				err = block(v.Then)
			}
			for _, branch := range v.ElseIfs {
				if err != nil {
					break
				}
				if err = expr(branch.Cond); err == nil {
					err = block(branch.Body)
				}
			}
			if err == nil {
				err = block(v.Else)
			}
		default:
			return fmt.Errorf("row source: оператор не входит в readonly профиль")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *program) expr(value ast.Expr, nodes *int, depth int) (err error) {
	if err := sourceBudget(nodes, depth); err != nil {
		return err
	}
	ordinal := *nodes
	defer func() {
		if err == nil && p.evidence != nil {
			err = p.evidence.recordLiteral(value, ordinal)
		}
	}()
	child := func(e ast.Expr) error { return p.expr(e, nodes, depth+1) }
	args := func(values []ast.Expr) error {
		for _, e := range values {
			if err := child(e); err != nil {
				return err
			}
		}
		return nil
	}
	switch v := value.(type) {
	case *ast.Ident:
		if strings.HasPrefix(v.Tok.Literal, "__") {
			return fmt.Errorf("row source: внутреннее имя host недоступно")
		}
		return nil
	case *ast.BoolLit, *ast.DateLit, *ast.MissingArg:
		return nil
	case *ast.StringLit:
		if len(v.Value) > 64<<10 {
			return fmt.Errorf("row source: слишком длинный литерал")
		}
		return nil
	case *ast.NumberLit:
		if len(v.Value) > 128 {
			return fmt.Errorf("row source: числовой литерал слишком длинный")
		}
		d, err := decimal.NewFromString(v.Value)
		if err != nil || !interpreter.DecimalWithinSafeBounds(d) {
			return fmt.Errorf("row source: число вне безопасного диапазона")
		}
		return nil
	case *ast.BinaryExpr:
		p.operations["operator:"+v.Op.Literal] = true
		if err := child(v.Left); err != nil {
			return err
		}
		return child(v.Right)
	case *ast.UnaryExpr:
		p.operations["operator:"+v.Op.Literal] = true
		return child(v.Operand)
	case *ast.TernaryExpr:
		if err := child(v.Cond); err != nil {
			return err
		}
		if err := child(v.True); err != nil {
			return err
		}
		return child(v.False)
	case *ast.MemberExpr:
		return child(v.Object)
	case *ast.IndexExpr:
		if err := child(v.Object); err != nil {
			return err
		}
		return child(v.Index)
	case *ast.ArrayLit:
		return args(v.Elements)
	case *ast.NewExpr:
		p.operations["new:"+strings.ToLower(v.TypeName.Literal)] = true
		if !dataConstructors[strings.ToLower(v.TypeName.Literal)] {
			return fmt.Errorf("row source: конструктор запрещён")
		}
		return args(v.Args)
	case *ast.CallExpr:
		switch callee := v.Callee.(type) {
		case *ast.Ident:
			name := strings.ToLower(callee.Tok.Literal)
			p.operations["function:"+name] = true
			if !pureFunctions[name] && p.procedures[name] == nil {
				return fmt.Errorf("row source: функция не доказана чистой")
			}
		case *ast.MemberExpr:
			p.operations["method:"+strings.ToLower(callee.Field.Literal)] = true
			if !dataMethods[strings.ToLower(callee.Field.Literal)] {
				return fmt.Errorf("row source: метод запрещён")
			}
			if err := child(callee.Object); err != nil {
				return err
			}
		default:
			return fmt.Errorf("row source: косвенный вызов запрещён")
		}
		return args(v.Args)
	default:
		return fmt.Errorf("row source: выражение не доказано безопасным")
	}
}

func (p *program) operationNames() []string {
	names := make([]string, 0, len(p.operations))
	for name := range p.operations {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
