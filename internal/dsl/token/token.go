package token

import "strings"

type Type int

const (
	ILLEGAL Type = iota
	EOF

	IDENT
	STRING
	NUMBER

	PROCEDURE
	ENDPROCEDURE
	FUNCTION
	ENDFUNCTION
	IF
	THEN
	ELSE
	ELSEIF
	ENDIF
	VAR
	FOR
	EACH
	IN
	DO
	ENDDO
	TO       // To / По  (numeric for loop upper bound)
	RETURN   // Return / Возврат
	NEW      // Новый / New
	AND      // И / And
	OR       // ИЛИ / Or
	NOT      // НЕ / Not
	TRUE     // Истина / True
	FALSE    // Ложь / False
	TRY      // Попытка / Try
	EXCEPT   // Исключение / Except
	ENDTRY   // КонецПопытки / EndTry
	BREAK    // Прервать / Break
	CONTINUE // Продолжить / Continue
	WHILE    // Пока / While

	ASSIGN  // =
	NEQ     // <>
	LT      // <
	GT      // >
	LTE     // <=
	GTE     // >=
	PLUS    // +
	MINUS   // -
	STAR    // *
	SLASH   // /
	PERCENT // % — остаток от деления

	PLUS_ASSIGN  // +=
	MINUS_ASSIGN // -=
	STAR_ASSIGN  // *=
	SLASH_ASSIGN // /=

	QUESTION // ?

	DOT
	COMMA
	SEMICOLON
	LPAREN
	RPAREN
	LBRACKET // [
	RBRACKET // ]
)

var keywords = map[string]Type{
	// English (all lowercase)
	"procedure":    PROCEDURE,
	"endprocedure": ENDPROCEDURE,
	"function":     FUNCTION,
	"endfunction":  ENDFUNCTION,
	"if":           IF,
	"then":         THEN,
	"else":         ELSE,
	"elseif":       ELSEIF,
	"endif":        ENDIF,
	"var":          VAR,
	"for":          FOR,
	"each":         EACH,
	"in":           IN,
	"do":           DO,
	"enddo":        ENDDO,
	"to":           TO,
	"return":       RETURN,
	// Русский (все в нижнем регистре)
	"процедура":      PROCEDURE,
	"конецпроцедуры": ENDPROCEDURE,
	"функция":        FUNCTION,
	"конецфункции":   ENDFUNCTION,
	"если":           IF,
	"тогда":          THEN,
	"иначе":          ELSE,
	"иначеесли":      ELSEIF,
	"конецесли":      ENDIF,
	"перем":          VAR,
	"для":            FOR,
	"каждого":        EACH,
	"из":             IN,
	"цикл":           DO,
	"конеццикла":     ENDDO,
	"по":             TO,
	"возврат":        RETURN,
	// новый / new
	"новый": NEW,
	"new":   NEW,
	// логика
	"и":   AND,
	"and": AND,
	"или": OR,
	"or":  OR,
	"не":  NOT,
	"not": NOT,
	// булевы литералы
	"истина": TRUE,
	"true":   TRUE,
	"ложь":   FALSE,
	"false":  FALSE,
	// попытка / исключение
	"попытка":      TRY,
	"try":          TRY,
	"исключение":   EXCEPT,
	"except":       EXCEPT,
	"конецпопытки": ENDTRY,
	"endtry":       ENDTRY,
	// прервать / продолжить
	"прервать":   BREAK,
	"break":      BREAK,
	"продолжить": CONTINUE,
	"continue":   CONTINUE,
	// пока / while
	"пока":  WHILE,
	"while": WHILE,
}

type Token struct {
	Type    Type
	Literal string
	File    string
	Line    int
	Col     int
}

func LookupIdent(ident string) Type {
	if t, ok := keywords[strings.ToLower(ident)]; ok {
		return t
	}
	return IDENT
}

// keywordTypes — множество типов-ключевых слов, построенное из карты keywords.
// Самоподдерживается: любое слово, добавленное в keywords, попадает сюда.
var keywordTypes = func() map[Type]bool {
	m := make(map[Type]bool, len(keywords))
	for _, t := range keywords {
		m[t] = true
	}
	return m
}()

// IsKeyword сообщает, является ли тип зарезервированным ключевым словом. Нужно
// для «контекстных» позиций (имя свойства/метода после точки), где ключевое
// слово допустимо как обычный идентификатор.
func IsKeyword(t Type) bool { return keywordTypes[t] }

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
	case FUNCTION:
		return "Function"
	case ENDFUNCTION:
		return "EndFunction"
	case IF:
		return "If"
	case THEN:
		return "Then"
	case ELSE:
		return "Else"
	case ELSEIF:
		return "ElseIf"
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
	case TO:
		return "To"
	case RETURN:
		return "Return"
	case NEW:
		return "Новый"
	case AND:
		return "И"
	case OR:
		return "ИЛИ"
	case NOT:
		return "НЕ"
	case TRUE:
		return "Истина"
	case FALSE:
		return "Ложь"
	case TRY:
		return "Попытка"
	case EXCEPT:
		return "Исключение"
	case ENDTRY:
		return "КонецПопытки"
	case BREAK:
		return "Прервать"
	case CONTINUE:
		return "Продолжить"
	case WHILE:
		return "Пока"
	case LBRACKET:
		return "["
	case RBRACKET:
		return "]"
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
	case PERCENT:
		return "%"
	case PLUS_ASSIGN:
		return "+="
	case MINUS_ASSIGN:
		return "-="
	case STAR_ASSIGN:
		return "*="
	case SLASH_ASSIGN:
		return "/="
	case QUESTION:
		return "?"
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
