package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/report/compose"
	"github.com/ivantit66/onebase/internal/report/expreval"
)

// CheckPIIOpenWhen проверяет условия раскрытия ПДн (pii_open_when). Условие
// вычисляется тем же вычислителем, что и в рантайме, на строке с пустыми
// значениями всех реквизитов шапки, а имена в нём сверяются с реквизитами:
// неизвестное имя DSL считает Неопределено, и опечатка («Статуc») молча
// делала бы условие ложным. В рантайме ошибочное условие оставляет маску —
// поле просто не открылось бы, и причину пришлось бы искать по журналу.
func CheckPIIOpenWhen(proj *project.Project) []Issue {
	var issues []Issue
	var ev *expreval.Evaluator
	for _, ent := range proj.Entities {
		if ent == nil {
			continue
		}
		var row compose.Row
		for _, f := range ent.Fields {
			if f.PIIOpenWhen == "" {
				continue
			}
			if ev == nil {
				ev = expreval.New(nil, expreval.DefaultProfile())
			}
			if row == nil {
				row = piiOpenWhenProbeRow(ent)
			}
			err := piiOpenWhenUnknownNames(ent, f.PIIOpenWhen)
			if err == nil {
				_, err = ev.EvalBool(f.PIIOpenWhen, row)
			}
			if err != nil {
				issues = append(issues, Issue{
					File:    entityFileLabel(ent),
					Object:  ent.Name,
					Kind:    "Реквизит",
					Code:    "field.pii-open-when",
					Message: "поле " + f.Name + ": ошибка условия pii_open_when \"" + f.PIIOpenWhen + "\": " + err.Error(),
				})
			}
		}
	}
	return issues
}

// piiOpenWhenProbeRow — реквизиты шапки с пустыми значениями своего типа: в
// рантайме условие видит реквизиты сохранённой записи.
func piiOpenWhenProbeRow(ent *metadata.Entity) compose.Row {
	row := compose.Row{}
	for _, f := range ent.Fields {
		switch f.Type {
		case metadata.FieldTypeNumber:
			row[f.Name] = 0.0
		case metadata.FieldTypeBool:
			row[f.Name] = false
		default:
			row[f.Name] = ""
		}
	}
	return row
}

// piiOpenWhenUnknownNames — имена-переменные условия, которых нет среди
// реквизитов шапки. Имена вызываемых функций и Неопределено/NULL — не реквизиты.
func piiOpenWhenUnknownNames(ent *metadata.Entity, expr string) error {
	src := "Функция __cond()\nВозврат (" + expr + ");\nКонецФункции\n"
	prog, err := parser.New(lexer.New(src, "cond.os")).ParseProgram()
	if err != nil {
		return err
	}
	if len(prog.Procedures) == 0 || len(prog.Procedures[0].Body) != 1 {
		return fmt.Errorf("условие должно быть одним выражением")
	}
	ret, ok := prog.Procedures[0].Body[0].(*ast.ReturnStmt)
	if !ok || ret.Value == nil {
		return fmt.Errorf("условие должно быть одним выражением")
	}
	known := map[string]bool{"неопределено": true, "undefined": true, "null": true}
	for _, f := range ent.Fields {
		known[strings.ToLower(f.Name)] = true
	}
	var unknown []string
	var walk func(e ast.Expr)
	walk = func(e ast.Expr) {
		switch n := e.(type) {
		case *ast.Ident:
			if !known[strings.ToLower(n.Tok.Literal)] {
				unknown = append(unknown, n.Tok.Literal)
			}
		case *ast.BinaryExpr:
			walk(n.Left)
			walk(n.Right)
		case *ast.UnaryExpr:
			walk(n.Operand)
		case *ast.CallExpr:
			if _, fn := n.Callee.(*ast.Ident); !fn {
				walk(n.Callee)
			}
			for _, a := range n.Args {
				walk(a)
			}
		case *ast.MemberExpr:
			walk(n.Object)
		case *ast.IndexExpr:
			walk(n.Object)
			walk(n.Index)
		case *ast.TernaryExpr:
			walk(n.Cond)
			walk(n.True)
			walk(n.False)
		case *ast.ArrayLit:
			for _, el := range n.Elements {
				walk(el)
			}
		}
	}
	walk(ret.Value)
	if len(unknown) > 0 {
		return fmt.Errorf("нет реквизита %s", strings.Join(unknown, ", "))
	}
	return nil
}
