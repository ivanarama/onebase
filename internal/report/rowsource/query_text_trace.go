package rowsource

import (
	"fmt"
	"io"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/token"
	"github.com/ivantit66/onebase/internal/provenance"
)

// This ledger binds actual setters to host AST sites and actual Query objects.
// It deliberately retains unresolved control/data lineage. It is NOT a producer
// receipt, literal authorization, final schema or a declassification mechanism.
type queryTextBinding struct {
	object, version               int
	site                          int
	lexical                       *literalEvidence
	controlRequired, dataRequired bool
}
type queryTextRead struct {
	binding          queryTextBinding
	text, parameters [32]byte
}
type queryExecutionEvidence struct {
	frame    *frameIdentity
	reads    []queryTextRead
	sqlReads []*guardedSQLProof // exact private receipts; inspection JSON cannot replace them
}

func (*queryExecutionEvidence) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "ReportQueryEvidence")
}

// Lower only validated plain member assignments. Argument order preserves the
// interpreter's RHS-before-receiver semantics and evaluates the receiver once.
// A user cannot call these names: compileProgram rejects __ identifiers before
// host lowering, and no dynamic evaluation is present in the pure profile.
func (p *program) bindTextSites(e *execution, vars map[string]any) {
	var lower func([]ast.Stmt)
	lower = func(body []ast.Stmt) {
		for index, stmt := range body {
			switch v := stmt.(type) {
			case *ast.AssignStmt:
				site, ok := p.evidence.textSites[v]
				if !ok || v.Op != token.ASSIGN {
					continue
				}
				member := v.Target.(*ast.MemberExpr)
				name := fmt.Sprintf("__rowsource_text_%d", site.node)
				original := v
				vars[name] = interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
					if len(args) != 2 {
						return nil, fmt.Errorf("row source: invalid private text binding")
					}
					value, receiver := interpreter.ToHostValue(args[0]), interpreter.ToHostValue(args[1])
					if q, ok := receiver.(*opaqueQuery); ok {
						if q == nil || q.execution != e {
							return nil, fmt.Errorf("row source: foreign Query receiver")
						}
						q.Set(member.Field.Literal, value)
						binding := queryTextBinding{object: q.identity, version: q.textVersion, site: site.node, controlRequired: true, dataRequired: true}
						// Recheck the exact original AST node; matching runtime bytes alone can
						// never establish lexical evidence for a variable/concat/function return.
						if site.literal != nil {
							proof, valid := p.evidence.literal(original.Value)
							if !valid || proof != *site.literal {
								return nil, fmt.Errorf("row source: stale lexical text site")
							}
							digest, err := provenance.InputDigest(value)
							if err != nil || digest != proof.value {
								return nil, fmt.Errorf("row source: lexical text binding changed")
							}
							binding.lexical = &proof
							binding.dataRequired = false // control remains unresolved, even for direct literal
						}
						q.textBinding = binding
					} else {
						// Text is also an ordinary structure property. Do not turn it into a
						// Query by spelling, alias, string contents or a returned descriptor.
						switch target := receiver.(type) {
						case interpreter.This:
							target.Set(strings.ToLower(member.Field.Literal), value)
						case *interpreter.Map:
							interpreter.RaiseUserError("row source: Соответствие не поддерживает запись по точке")
						}
					}
					return nil, nil
				})
				ident := token.Token{Type: token.IDENT, Literal: name, File: member.Field.File, Line: member.Field.Line, Col: member.Field.Col}
				body[index] = &ast.ExprStmt{X: &ast.CallExpr{Callee: &ast.Ident{Tok: ident}, Args: []ast.Expr{v.Value, member.Object}}}
			case *ast.IfStmt:
				lower(v.Then)
				for _, branch := range v.ElseIfs {
					lower(branch.Body)
				}
				lower(v.Else)
			case *ast.ForEachStmt:
				lower(v.Body)
			case *ast.NumericForStmt:
				lower(v.Body)
			}
		}
	}
	for _, proc := range p.procedures {
		lower(proc.Body)
	}
}

func (q *opaqueQuery) recordTextRead(params map[string]any) error {
	if len(q.execution.textReads) >= maxQueries {
		return fmt.Errorf("row source: query text trace budget exceeded")
	}
	text, err := provenance.InputDigest(q.text)
	if err != nil {
		return err
	}
	parameters, err := provenance.InputDigest(params)
	if err != nil {
		return err
	}
	binding := q.textBinding
	if binding.object != q.identity || binding.version != q.textVersion {
		return fmt.Errorf("row source: stale Query text binding")
	}
	q.execution.textReads = append(q.execution.textReads, queryTextRead{binding: binding, text: text, parameters: parameters})
	return nil
}
