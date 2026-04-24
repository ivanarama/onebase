package token

type Type int

const (
	ILLEGAL Type = iota
	EOF

	IDENT
	STRING
	NUMBER

	PROCEDURE
	ENDPROCEDURE
	IF
	THEN
	ELSE
	ENDIF
	VAR
	FOR
	EACH
	IN
	DO
	ENDDO
	TO     // To / По  (numeric for loop upper bound)
	RETURN // Return / Возврат

	ASSIGN // =
	NEQ    // <>
	LT     // <
	GT     // >
	LTE    // <=
	GTE    // >=
	PLUS   // +
	MINUS  // -
	STAR   // *
	SLASH  // /

	DOT
	COMMA
	SEMICOLON
	LPAREN
	RPAREN
)

var keywords = map[string]Type{
	// English
	"Procedure":    PROCEDURE,
	"EndProcedure": ENDPROCEDURE,
	"If":           IF,
	"Then":         THEN,
	"Else":         ELSE,
	"EndIf":        ENDIF,
	"Var":          VAR,
	"For":          FOR,
	"Each":         EACH,
	"In":           IN,
	"Do":           DO,
	"EndDo":        ENDDO,
	"To":           TO,
	"Return":       RETURN,
	// Русский
	"Процедура":      PROCEDURE,
	"КонецПроцедуры": ENDPROCEDURE,
	"Если":           IF,
	"Тогда":          THEN,
	"Иначе":          ELSE,
	"КонецЕсли":      ENDIF,
	"Перем":          VAR,
	"Для":            FOR,
	"Каждого":        EACH,
	"Из":             IN,
	"Цикл":           DO,
	"КонецЦикла":     ENDDO,
	"По":             TO,
	"Возврат":        RETURN,
}

type Token struct {
	Type    Type
	Literal string
	File    string
	Line    int
	Col     int
}

func LookupIdent(ident string) Type {
	if t, ok := keywords[ident]; ok {
		return t
	}
	return IDENT
}

func (t Type) String() string {
	switch t {
	case ILLEGAL:
		return "ILLEGAL"
	case EOF:
		return "EOF"
	case IDENT:
		return "IDENT"
	case STRING:
		return "STRING"
	case NUMBER:
		return "NUMBER"
	case PROCEDURE:
		return "Procedure"
	case ENDPROCEDURE:
		return "EndProcedure"
	case IF:
		return "If"
	case THEN:
		return "Then"
	case ELSE:
		return "Else"
	case ENDIF:
		return "EndIf"
	case VAR:
		return "Var"
	case FOR:
		return "For"
	case EACH:
		return "Each"
	case IN:
		return "In"
	case DO:
		return "Do"
	case ENDDO:
		return "EndDo"
	case ASSIGN:
		return "="
	case NEQ:
		return "<>"
	case LT:
		return "<"
	case GT:
		return ">"
	case LTE:
		return "<="
	case GTE:
		return ">="
	case PLUS:
		return "+"
	case MINUS:
		return "-"
	case STAR:
		return "*"
	case SLASH:
		return "/"
	case DOT:
		return "."
	case COMMA:
		return ","
	case SEMICOLON:
		return ";"
	case LPAREN:
		return "("
	case RPAREN:
		return ")"
	default:
		return "UNKNOWN"
	}
}
