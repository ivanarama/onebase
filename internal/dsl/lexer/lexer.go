package lexer

import (
	"unicode"

	"github.com/ivantit66/onebase/internal/dsl/token"
)

type Lexer struct {
	input []rune
	file  string
	pos   int
	line  int
	col   int
}

func New(input, file string) *Lexer {
	return &Lexer{input: []rune(input), file: file, line: 1, col: 1}
}

func (l *Lexer) peek() rune {
	if l.pos >= len(l.input) {
		return 0
	}
	return l.input[l.pos]
}

func (l *Lexer) next() rune {
	r := l.input[l.pos]
	l.pos++
	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return r
}

func (l *Lexer) skip() {
	for l.pos < len(l.input) {
		r := l.peek()
		switch {
		case r == ' ' || r == '\t' || r == '\r' || r == '\n':
			l.next()
		case r == '/' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '/':
			for l.pos < len(l.input) && l.peek() != '\n' {
				l.next()
			}
		default:
			return
		}
	}
}

func (l *Lexer) tok(t token.Type, lit string, line, col int) token.Token {
	return token.Token{Type: t, Literal: lit, File: l.file, Line: line, Col: col}
}

func (l *Lexer) NextToken() token.Token {
	l.skip()
	if l.pos >= len(l.input) {
		return l.tok(token.EOF, "", l.line, l.col)
	}
	line, col := l.line, l.col
	r := l.next()
	switch r {
	case '.':
		return l.tok(token.DOT, ".", line, col)
	case ',':
		return l.tok(token.COMMA, ",", line, col)
	case ';':
		return l.tok(token.SEMICOLON, ";", line, col)
	case '(':
		return l.tok(token.LPAREN, "(", line, col)
	case ')':
		return l.tok(token.RPAREN, ")", line, col)
	case '=':
		return l.tok(token.ASSIGN, "=", line, col)
	case '<':
		if l.pos < len(l.input) && l.peek() == '>' {
			l.next()
			return l.tok(token.NEQ, "<>", line, col)
		}
		if l.pos < len(l.input) && l.peek() == '=' {
			l.next()
			return l.tok(token.LTE, "<=", line, col)
		}
		return l.tok(token.LT, "<", line, col)
	case '>':
		if l.pos < len(l.input) && l.peek() == '=' {
			l.next()
			return l.tok(token.GTE, ">=", line, col)
		}
		return l.tok(token.GT, ">", line, col)
	case '"':
		start := l.pos
		for l.pos < len(l.input) && l.peek() != '"' {
			l.next()
		}
		s := string(l.input[start:l.pos])
		if l.pos < len(l.input) {
			l.next() // closing "
		}
		return l.tok(token.STRING, s, line, col)
	default:
		if isLetter(r) {
			start := l.pos - 1
			for l.pos < len(l.input) && (isLetter(l.peek()) || isDigit(l.peek())) {
				l.next()
			}
			lit := string(l.input[start:l.pos])
			return l.tok(token.LookupIdent(lit), lit, line, col)
		}
		if isDigit(r) {
			start := l.pos - 1
			for l.pos < len(l.input) && (isDigit(l.peek()) || l.peek() == '.') {
				l.next()
			}
			return l.tok(token.NUMBER, string(l.input[start:l.pos]), line, col)
		}
		return l.tok(token.ILLEGAL, string(r), line, col)
	}
}

func isLetter(r rune) bool {
	return unicode.IsLetter(r) || r == '_'
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}
