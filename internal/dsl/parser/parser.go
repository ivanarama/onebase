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
		if p.cur.Type != token.PROCEDURE && p.cur.Type != token.FUNCTION {
			return nil, fmt.Errorf("%s:%d:%d: expected Procedure or Function, got %q",
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
	isFunc := p.cur.Type == token.FUNCTION
	p.advance() // consume Procedure/Function
	nameTok, err := p.expect(token.IDENT)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(token.LPAREN); err != nil {
		return nil, err
	}
	var params []token.Token
	for p.cur.Type != token.RPAREN && p.cur.Type != token.EOF {
		paramTok, err := p.expect(token.IDENT)
		if err != nil {
			return nil, err
		}
		params = append(params, paramTok)
		if p.cur.Type == token.COMMA {
			p.advance()
		}
	}
	if _, err := p.expect(token.RPAREN); err != nil {
		return nil, err
	}
	endTok := token.ENDPROCEDURE
	if isFunc {
		endTok = token.ENDFUNCTION
	}
	body, err := p.parseBlock(endTok)
	if err != nil {
		return nil, err
	}
	p.advance() // consume EndProcedure/EndFunction
	return &ast.ProcedureDecl{Name: nameTok, Params: params, Body: body}, nil
}

// isBlockEnd returns true for tokens that end a block from the outside.
func isBlockEnd(t token.Type) bool {
	switch t {
	case token.EOF, token.ELSE, token.ENDIF, token.ENDDO, token.ENDPROCEDURE, token.ENDFUNCTION:
		return true
	}
	return false
}

func (p *Parser) parseBlock(end token.Type) ([]ast.Stmt, error) {
	var stmts []ast.Stmt
	for p.cur.Type != end && !isBlockEnd(p.cur.Type) {
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
	case token.FOR:
		// Для Каждого ... → ForEach
		// Для i = ... По ... → NumericFor
		if p.peek.Type == token.EACH {
			return p.parseForEach()
		}
		return p.parseNumericFor()
	case token.VAR:
		return p.parseVarDecl()
	case token.RETURN:
		return p.parseReturn()
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

func (p *Parser) parseForEach() (*ast.ForEachStmt, error) {
	p.advance() // consume For/Для
	if _, err := p.expect(token.EACH); err != nil {
		return nil, err
	}
	varTok, err := p.expect(token.IDENT)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(token.IN); err != nil {
		return nil, err
	}
	coll, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(token.DO); err != nil {
		return nil, err
	}
	body, err := p.parseBlock(token.ENDDO)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(token.ENDDO); err != nil {
		return nil, err
	}
	p.consumeSemi()
	return &ast.ForEachStmt{Var: varTok, Collection: coll, Body: body}, nil
}

// parseNumericFor разбирает: Для i = start По end Цикл ... КонецЦикла
func (p *Parser) parseNumericFor() (*ast.NumericForStmt, error) {
	p.advance() // consume Для/For
	varTok, err := p.expect(token.IDENT)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(token.ASSIGN); err != nil {
		return nil, err
	}
	start, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(token.TO); err != nil {
		return nil, err
	}
	end, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(token.DO); err != nil {
		return nil, err
	}
	body, err := p.parseBlock(token.ENDDO)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(token.ENDDO); err != nil {
		return nil, err
	}
	p.consumeSemi()
	return &ast.NumericForStmt{Var: varTok, Start: start, End: end, Body: body}, nil
}

// parseReturn разбирает: Возврат [expr];
func (p *Parser) parseReturn() (*ast.ReturnStmt, error) {
	tok := p.cur
	p.advance() // consume Возврат/Return
	// Нет значения если сразу ; или конец блока
	if p.cur.Type == token.SEMICOLON || p.cur.Type == token.EOF ||
		p.cur.Type == token.ENDIF || p.cur.Type == token.ENDDO ||
		p.cur.Type == token.ENDPROCEDURE {
		p.consumeSemi()
		return &ast.ReturnStmt{Tok: tok, Value: nil}, nil
	}
	val, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.consumeSemi()
	return &ast.ReturnStmt{Tok: tok, Value: val}, nil
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
// "left = right;" is assignment only when left is a simple Ident or MemberExpr.
func (p *Parser) parseExprOrAssign() (ast.Stmt, error) {
	left, err := p.parseMathExpr()
	if err != nil {
		return nil, err
	}

	if p.cur.Type == token.ASSIGN {
		switch left.(type) {
		case *ast.Ident, *ast.MemberExpr:
			p.advance() // consume =
			val, err := p.parseMathExpr()
			if err != nil {
				return nil, err
			}
			p.consumeSemi()
			return &ast.AssignStmt{Target: left, Value: val}, nil
		}
	}

	// comparison at statement level
	for isComparisonOp(p.cur.Type) {
		op := p.cur
		p.advance()
		right, err := p.parseMathExpr()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{Left: left, Op: op, Right: right}
	}
	p.consumeSemi()
	return &ast.ExprStmt{X: left}, nil
}

// parseExpr parses a full expression (used in conditions: If, ForEach).
// Comparisons have lower precedence than arithmetic.
func (p *Parser) parseExpr() (ast.Expr, error) {
	left, err := p.parseMathExpr()
	if err != nil {
		return nil, err
	}
	for isComparisonOp(p.cur.Type) {
		op := p.cur
		p.advance()
		right, err := p.parseMathExpr()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{Left: left, Op: op, Right: right}
	}
	return left, nil
}

// parseMathExpr handles + and - (additive, left-to-right).
func (p *Parser) parseMathExpr() (ast.Expr, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for p.cur.Type == token.PLUS || p.cur.Type == token.MINUS {
		op := p.cur
		p.advance()
		right, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{Left: left, Op: op, Right: right}
	}
	return left, nil
}

// parseTerm handles * and / (multiplicative, left-to-right).
func (p *Parser) parseTerm() (ast.Expr, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for p.cur.Type == token.STAR || p.cur.Type == token.SLASH {
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

func isComparisonOp(t token.Type) bool {
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
		var expr ast.Expr = &ast.Ident{Tok: tok}
		for {
			if p.cur.Type == token.LPAREN {
				p.advance()
				args, err := p.parseArgs()
				if err != nil {
					return nil, err
				}
				if _, err := p.expect(token.RPAREN); err != nil {
					return nil, err
				}
				expr = &ast.CallExpr{Callee: expr, Args: args}
			} else if p.cur.Type == token.DOT {
				p.advance()
				field, err := p.expect(token.IDENT)
				if err != nil {
					return nil, err
				}
				expr = &ast.MemberExpr{Object: expr, Field: field}
			} else {
				break
			}
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
