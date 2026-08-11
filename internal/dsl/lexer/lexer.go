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
	// Срезаем ведущий BOM (U+FEFF): файлы, сохранённые внешними редакторами
	// или выгруженные из 1С, часто начинаются с него, и без этого лексер
	// спотыкается на первом же токене ("expected Procedure or Function").
	runes := []rune(input)
	if len(runes) > 0 && runes[0] == '\uFEFF' {
		runes = runes[1:]
	}
	return &Lexer{input: runes, file: file, line: 1, col: 1}
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
		case r == '#' && l.directiveLineStart():
			// Директива препроцессора 1С (#Область, #Если…) в начале строки —
			// пропускаем строку как комментарий: конвертер 1С→OneBase вырезает
			// их сам, но копипаст из 1С не должен валить разбор (issue #48 п.2).
			// '#' в середине строки сюда НЕ попадает — он даст ILLEGAL в
			// NextToken (громкая ошибка вместо тихого съедания остатка строки).
			for l.pos < len(l.input) && l.peek() != '\n' {
				l.next()
			}
		default:
			return
		}
	}
}

// directiveLineStart сообщает, что текущий '#' стоит в начале строки (до него на
// этой строке только пробелы/табы). Тогда это директива препроцессора 1С, а не
// случайный '#' посреди выражения, который должен стать ILLEGAL.
func (l *Lexer) directiveLineStart() bool {
	for i := l.pos - 1; i >= 0; i-- {
		switch l.input[i] {
		case '\n':
			return true
		case ' ', '\t', '\r':
			continue
		default:
			return false
		}
	}
	return true // начало ввода
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
	case '[':
		return l.tok(token.LBRACKET, "[", line, col)
	case ']':
		return l.tok(token.RBRACKET, "]", line, col)
	case '+':
		if l.pos < len(l.input) && l.peek() == '=' {
			l.next()
			return l.tok(token.PLUS_ASSIGN, "+=", line, col)
		}
		return l.tok(token.PLUS, "+", line, col)
	case '-':
		if l.pos < len(l.input) && l.peek() == '=' {
			l.next()
			return l.tok(token.MINUS_ASSIGN, "-=", line, col)
		}
		return l.tok(token.MINUS, "-", line, col)
	case '%':
		return l.tok(token.PERCENT, "%", line, col)
	case '*':
		if l.pos < len(l.input) && l.peek() == '=' {
			l.next()
			return l.tok(token.STAR_ASSIGN, "*=", line, col)
		}
		return l.tok(token.STAR, "*", line, col)
	case '/':
		if l.pos < len(l.input) && l.peek() == '/' {
			for l.pos < len(l.input) && l.peek() != '\n' {
				l.next()
			}
			return l.NextToken()
		}
		if l.pos < len(l.input) && l.peek() == '=' {
			l.next()
			return l.tok(token.SLASH_ASSIGN, "/=", line, col)
		}
		return l.tok(token.SLASH, "/", line, col)
	case '=':
		return l.tok(token.ASSIGN, "=", line, col)
	case '?':
		return l.tok(token.QUESTION, "?", line, col)
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
		// "" inside a string literal is an escaped double-quote (1C convention).
		// Multi-line strings: a newline followed by optional whitespace and '|'
		// strips the whitespace+'|' (1C-style continuation marker).
		var buf []rune
		for l.pos < len(l.input) {
			ch := l.peek()
			if ch == '"' {
				l.next()
				if l.pos < len(l.input) && l.peek() == '"' {
					l.next()
					buf = append(buf, '"')
				} else {
					break
				}
			} else if ch == '\n' {
				buf = append(buf, l.next()) // keep the newline
				// Strip leading whitespace + '|' continuation marker
				for l.pos < len(l.input) && (l.peek() == ' ' || l.peek() == '\t') {
					l.next()
				}
				if l.pos < len(l.input) && l.peek() == '|' {
					l.next() // consume '|'
				}
			} else {
				buf = append(buf, l.next())
			}
		}
		return l.tok(token.STRING, string(buf), line, col)
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
