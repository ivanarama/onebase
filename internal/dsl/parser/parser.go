package parser

import (
	"fmt"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/token"
)

type Parser struct {
	l    *lexer.Lexer
	cur  token.Token
	peek token.Token
}

func New(l *lexer.Lexer) *Parser {
	p := &Parser{l: l}
	p.advance()
	p.advance()
	return p
}

func (p *Parser) advance() {
	p.cur = p.peek
	p.peek = p.l.NextToken()
}

func (p *Parser) expect(t token.Type) (token.Token, error) {
	if p.cur.Type != t {
		return p.cur, fmt.Errorf("%s:%d:%d: expected %s, got %q",
			p.cur.File, p.cur.Line, p.cur.Col, t, p.cur.Literal)
	}
	tok := p.cur
	p.advance()
	return tok, nil
}

func (p *Parser) consumeSemi() {
	if p.cur.Type == token.SEMICOLON {
		p.advance()
	}
}

func (p *Parser) ParseProgram() (*ast.Program, error) {
	prog := &ast.Program{}
	for p.cur.Type != token.EOF {
		if p.cur.Type != token.PROCEDURE {
			return nil, fmt.Errorf("%s:%d:%d: expected Procedure, got %q",
				p.cur.File, p.cur.Line, p.cur.Col, p.cur.Literal)
		}
		proc, err := p.parseProcedure()
		if err != nil {
			return nil, err
		}
		prog.Procedures = append(prog.Procedures, proc)
	}
	return prog, nil
}

func (p *Parser) parseProcedure() (*ast.ProcedureDecl, error) {
	p.advance() // consume Procedure
	nameTok, err := p.expect(token.IDENT)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(token.LPAREN); err != nil {
		return nil, err
	}
	if _, err := p.expect(token.RPAREN); err != nil {
		return nil, err
	}
	body, err := p.parseBlock(token.ENDPROCEDURE)
	if err != nil {
		return nil, err
	}
	p.advance() // consume EndProcedure
	return &ast.ProcedureDecl{Name: nameTok, Body: body}, nil
}

func (p *Parser) parseBlock(end token.Type) ([]ast.Stmt, error) {
	var stmts []ast.Stmt
	for p.cur.Type != end && p.cur.Type != token.EOF &&
		p.cur.Type != token.ELSE && p.cur.Type != token.ENDIF {
		stmt, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, stmt)
	}
	return stmts, nil
}

func (p *Parser) parseStmt() (ast.Stmt, error) {
	switch p.cur.Type {
	case token.IF:
		return p.parseIf()
	case token.VAR:
		return p.parseVarDecl()
	default:
		return p.parseExprOrAssign()
	}
}

func (p *Parser) parseIf() (*ast.IfStmt, error) {
	p.advance() // consume If
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(token.THEN); err != nil {
		return nil, err
	}
	then, err := p.parseBlock(token.ENDIF)
	if err != nil {
		return nil, err
	}
	var els []ast.Stmt
	if p.cur.Type == token.ELSE {
		p.advance()
		els, err = p.parseBlock(token.ENDIF)
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(token.ENDIF); err != nil {
		return nil, err
	}
	p.consumeSemi()
	return &ast.IfStmt{Cond: cond, Then: then, Else: els}, nil
}

func (p *Parser) parseVarDecl() (*ast.VarDecl, error) {
	p.advance() // consume Var
	nameTok, err := p.expect(token.IDENT)
	if err != nil {
		return nil, err
	}
	p.consumeSemi()
	return &ast.VarDecl{Name: nameTok}, nil
}

// parseExprOrAssign disambiguates assignment vs expression statement.
// In 1C, "=" means both assign (statement) and equals (condition).
// We treat "left = right;" as assignment only when left is Ident or MemberExpr.
func (p *Parser) parseExprOrAssign() (ast.Stmt, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}

	if p.cur.Type == token.ASSIGN {
		switch left.(type) {
		case *ast.Ident, *ast.MemberExpr:
			p.advance() // consume =
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			p.consumeSemi()
			return &ast.AssignStmt{Target: left, Value: val}, nil
		}
	}

	// binary expression continuation
	for isBinaryOp(p.cur.Type) {
		op := p.cur
		p.advance()
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{Left: left, Op: op, Right: right}
	}
	p.consumeSemi()
	return &ast.ExprStmt{X: left}, nil
}

// parseExpr is used in condition contexts where "=" always means equality.
func (p *Parser) parseExpr() (ast.Expr, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for isBinaryOp(p.cur.Type) {
		op := p.cur
		p.advance()
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{Left: left, Op: op, Right: right}
	}
	return left, nil
}

func isBinaryOp(t token.Type) bool {
	switch t {
	case token.ASSIGN, token.NEQ, token.LT, token.GT, token.LTE, token.GTE:
		return true
	}
	return false
}

func (p *Parser) parsePrimary() (ast.Expr, error) {
	switch p.cur.Type {
	case token.STRING:
		tok := p.cur
		p.advance()
		return &ast.StringLit{Tok: tok, Value: tok.Literal}, nil
	case token.NUMBER:
		tok := p.cur
		p.advance()
		return &ast.NumberLit{Tok: tok, Value: tok.Literal}, nil
	case token.IDENT:
		tok := p.cur
		p.advance()
		if p.cur.Type == token.LPAREN {
			p.advance()
			args, err := p.parseArgs()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(token.RPAREN); err != nil {
				return nil, err
			}
			return &ast.CallExpr{Callee: tok, Args: args}, nil
		}
		var expr ast.Expr = &ast.Ident{Tok: tok}
		for p.cur.Type == token.DOT {
			p.advance()
			field, err := p.expect(token.IDENT)
			if err != nil {
				return nil, err
			}
			expr = &ast.MemberExpr{Object: expr, Field: field}
		}
		return expr, nil
	default:
		return nil, fmt.Errorf("%s:%d:%d: unexpected %q in expression",
			p.cur.File, p.cur.Line, p.cur.Col, p.cur.Literal)
	}
}

func (p *Parser) parseArgs() ([]ast.Expr, error) {
	var args []ast.Expr
	if p.cur.Type == token.RPAREN {
		return args, nil
	}
	arg, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	args = append(args, arg)
	for p.cur.Type == token.COMMA {
		p.advance()
		arg, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
	}
	return args, nil
}
