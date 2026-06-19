package parser

import (
	"fmt"
	"strings"

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

func (p *Parser) expectSemicolon() {
	p.consumeSemi()
}

func (p *Parser) ParseProgram() (*ast.Program, error) {
	prog := &ast.Program{}
	// Раздел объявления переменных модуля (как в 1С): ноль или более «Перем …»
	// до процедур и функций. Модули объекта из выгрузок 1С начинаются с него,
	// иначе парсер падал на «expected Procedure or Function, got "Перем"» (issue #115).
	for p.cur.Type == token.VAR {
		vd, err := p.parseVarDecl()
		if err != nil {
			return nil, err
		}
		prog.ModuleVars = append(prog.ModuleVars, vd)
	}
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
	var defaults []ast.Expr
	for p.cur.Type != token.RPAREN && p.cur.Type != token.EOF {
		paramTok, err := p.expect(token.IDENT)
		if err != nil {
			return nil, err
		}
		params = append(params, paramTok)
		// Опциональное значение по умолчанию: ИмяПарам = expr (см.
		var def ast.Expr
		if p.cur.Type == token.ASSIGN {
			p.advance() // consume =
			d, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			def = d
		}
		defaults = append(defaults, def)
		if p.cur.Type == token.COMMA {
			p.advance()
		}
	}
	if _, err := p.expect(token.RPAREN); err != nil {
		return nil, err
	}
	// Опциональный модификатор «Экспорт» после сигнатуры (как в 1С). Лексер
	// токенизирует его как IDENT, поэтому распознаём по литералу и пропускаем —
	// иначе он осел бы фиктивным выражением-без-эффекта в начале тела.
	if p.cur.Type == token.IDENT {
		if low := strings.ToLower(p.cur.Literal); low == "экспорт" || low == "export" {
			p.advance()
		}
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
	return &ast.ProcedureDecl{Name: nameTok, Params: params, Defaults: defaults, Body: body}, nil
}

// isBlockEnd returns true for tokens that end a block from the outside.
func isBlockEnd(t token.Type) bool {
	switch t {
	case token.EOF, token.ELSE, token.ELSEIF, token.ENDIF, token.ENDDO, token.ENDPROCEDURE, token.ENDFUNCTION,
		token.EXCEPT, token.ENDTRY:
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
	case token.WHILE:
		return p.parseWhile()
	case token.VAR:
		return p.parseVarDecl()
	case token.RETURN:
		return p.parseReturn()
	case token.TRY:
		return p.parseTry()
	case token.BREAK:
		tok := p.cur
		p.advance()
		p.expectSemicolon()
		return &ast.BreakStmt{Tok: tok}, nil
	case token.CONTINUE:
		tok := p.cur
		p.advance()
		p.expectSemicolon()
		return &ast.ContinueStmt{Tok: tok}, nil
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
	var elseIfs []ast.ElseIfBranch
	for p.cur.Type == token.ELSEIF {
		p.advance() // consume ElseIf
		elifCond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(token.THEN); err != nil {
			return nil, err
		}
		elifBody, err := p.parseBlock(token.ENDIF)
		if err != nil {
			return nil, err
		}
		elseIfs = append(elseIfs, ast.ElseIfBranch{Cond: elifCond, Body: elifBody})
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
	return &ast.IfStmt{Cond: cond, Then: then, ElseIfs: elseIfs, Else: els}, nil
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

// parseWhile разбирает: Пока <условие> Цикл ... КонецЦикла
func (p *Parser) parseWhile() (*ast.WhileStmt, error) {
	tok := p.cur
	p.advance() // consume Пока/While
	cond, err := p.parseExpr()
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
	return &ast.WhileStmt{Tok: tok, Cond: cond, Body: body}, nil
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
	names := []token.Token{nameTok}
	// Несколько переменных в одном объявлении через запятую: Перем а, б, в; (как в 1С).
	for p.cur.Type == token.COMMA {
		p.advance() // consume ,
		nameTok, err := p.expect(token.IDENT)
		if err != nil {
			return nil, err
		}
		names = append(names, nameTok)
	}
	// Опциональный модификатор «Экспорт» (переменные модуля 1С: Перем X Экспорт;).
	// Лексер токенизирует его как IDENT — распознаём по литералу, как parseProcedure.
	exported := false
	if p.cur.Type == token.IDENT {
		if low := strings.ToLower(p.cur.Literal); low == "экспорт" || low == "export" {
			exported = true
			p.advance()
		}
	}
	p.consumeSemi()
	return &ast.VarDecl{Names: names, Exported: exported}, nil
}

func isCompoundAssign(t token.Type) bool {
	switch t {
	case token.PLUS_ASSIGN, token.MINUS_ASSIGN, token.STAR_ASSIGN, token.SLASH_ASSIGN:
		return true
	}
	return false
}

// parseExprOrAssign disambiguates assignment vs expression statement.
// "left = right;" is assignment only when left is a simple Ident, MemberExpr or IndexExpr.
func (p *Parser) parseExprOrAssign() (ast.Stmt, error) {
	left, err := p.parseMathExpr()
	if err != nil {
		return nil, err
	}

	if p.cur.Type == token.ASSIGN {
		switch left.(type) {
		case *ast.Ident, *ast.MemberExpr, *ast.IndexExpr:
			p.advance() // consume =
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			p.consumeSemi()
			return &ast.AssignStmt{Target: left, Op: token.ASSIGN, Value: val}, nil
		}
	}
	if isCompoundAssign(p.cur.Type) {
		switch left.(type) {
		case *ast.Ident, *ast.MemberExpr, *ast.IndexExpr:
			op := p.cur.Type
			p.advance()
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			p.consumeSemi()
			return &ast.AssignStmt{Target: left, Op: op, Value: val}, nil
		}
	}

	// boolean / comparison at statement level
	for isComparisonOp(p.cur.Type) || p.cur.Type == token.AND || p.cur.Type == token.OR {
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

// ParseExpr parses a standalone expression. Exported for the debugger console.
func (p *Parser) ParseExpr() (ast.Expr, error) {
	return p.parseExpr()
}

// parseExpr parses a full expression (conditions, right-hand of assignments).
// Precedence (low → high): OR → AND → NOT → comparison → additive → term → primary
func (p *Parser) parseExpr() (ast.Expr, error) {
	return p.parseOr()
}

func (p *Parser) parseOr() (ast.Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.cur.Type == token.OR {
		op := p.cur
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{Left: left, Op: op, Right: right}
	}
	return left, nil
}

func (p *Parser) parseAnd() (ast.Expr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.cur.Type == token.AND {
		op := p.cur
		p.advance()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{Left: left, Op: op, Right: right}
	}
	return left, nil
}

func (p *Parser) parseNot() (ast.Expr, error) {
	if p.cur.Type == token.NOT {
		op := p.cur
		p.advance()
		operand, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		return &ast.UnaryExpr{Op: op, Operand: operand}, nil
	}
	return p.parseComparison()
}

func (p *Parser) parseComparison() (ast.Expr, error) {
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
	case token.TRUE:
		tok := p.cur
		p.advance()
		return &ast.BoolLit{Tok: tok, Value: true}, nil
	case token.FALSE:
		tok := p.cur
		p.advance()
		return &ast.BoolLit{Tok: tok, Value: false}, nil
	case token.MINUS:
		// унарный минус
		op := p.cur
		p.advance()
		operand, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &ast.UnaryExpr{Op: op, Operand: operand}, nil
	case token.LPAREN:
		// группировка: ( expr )
		p.advance()
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(token.RPAREN); err != nil {
			return nil, err
		}
		return expr, nil
	case token.NEW:
		return p.parseNew()
	case token.QUESTION:
		tok := p.cur
		p.advance() // consume ?
		if _, err := p.expect(token.LPAREN); err != nil {
			return nil, err
		}
		cond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(token.COMMA); err != nil {
			return nil, err
		}
		trueVal, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(token.COMMA); err != nil {
			return nil, err
		}
		falseVal, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(token.RPAREN); err != nil {
			return nil, err
		}
		return &ast.TernaryExpr{Tok: tok, Cond: cond, True: trueVal, False: falseVal}, nil
	case token.IDENT:
		tok := p.cur
		p.advance()
		var expr ast.Expr = &ast.Ident{Tok: tok}
		expr = p.parsePostfix(expr)
		return expr, nil
	default:
		return nil, fmt.Errorf("%s:%d:%d: unexpected %q in expression",
			p.cur.File, p.cur.Line, p.cur.Col, p.cur.Literal)
	}
}

// parseTry разбирает: Попытка <stmts> Исключение <stmts> КонецПопытки
func (p *Parser) parseTry() (*ast.TryStmt, error) {
	tok := p.cur
	p.advance() // consume Попытка
	tryBody, err := p.parseBlock(token.ENDTRY)
	if err != nil {
		return nil, err
	}
	var exceptBody []ast.Stmt
	if p.cur.Type == token.EXCEPT {
		p.advance()
		exceptBody, err = p.parseBlock(token.ENDTRY)
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(token.ENDTRY); err != nil {
		return nil, err
	}
	p.consumeSemi()
	return &ast.TryStmt{Tok: tok, Try: tryBody, Except: exceptBody}, nil
}

// parseNew разбирает: Новый ТипКоллекции[(args)]
func (p *Parser) parseNew() (ast.Expr, error) {
	p.advance() // consume Новый/New
	typeTok, err := p.expect(token.IDENT)
	if err != nil {
		return nil, err
	}
	var args []ast.Expr
	if p.cur.Type == token.LPAREN {
		p.advance()
		args, err = p.parseArgs()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(token.RPAREN); err != nil {
			return nil, err
		}
	}
	expr := ast.Expr(&ast.NewExpr{TypeName: typeTok, Args: args})
	expr = p.parsePostfix(expr)
	return expr, nil
}

// parsePostfix обрабатывает цепочки .поле, (args), [idx] после primary.
func (p *Parser) parsePostfix(expr ast.Expr) ast.Expr {
	for {
		switch p.cur.Type {
		case token.LPAREN:
			p.advance()
			args, err := p.parseArgs()
			if err != nil {
				return expr
			}
			if _, err2 := p.expect(token.RPAREN); err2 != nil {
				return expr
			}
			expr = &ast.CallExpr{Callee: expr, Args: args}
		case token.DOT:
			p.advance()
			field, err := p.expect(token.IDENT)
			if err != nil {
				return expr
			}
			expr = &ast.MemberExpr{Object: expr, Field: field}
		case token.LBRACKET:
			p.advance()
			idx, err := p.parseExpr()
			if err != nil {
				return expr
			}
			if _, err2 := p.expect(token.RBRACKET); err2 != nil {
				return expr
			}
			expr = &ast.IndexExpr{Object: expr, Index: idx}
		default:
			return expr
		}
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
