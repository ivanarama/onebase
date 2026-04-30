package query

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Result holds compiled PostgreSQL SQL and positional arguments.
type Result struct {
	SQL  string
	Args []any
}

// Compile translates a 1C-style query to PostgreSQL SQL.
// paramValues maps &ParamName → value (nil becomes SQL NULL).
func Compile(src string, paramValues map[string]any) (Result, error) {
	return translate(tokenize(src), paramValues)
}

// --- tokenizer ---

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tDot
	tComma
	tLParen
	tRParen
	tParam
	tStr
	tNum
	tOp
	tStar
)

type tok struct {
	kind tokKind
	val  string
}

func tokenize(src string) []tok {
	var out []tok
	runes := []rune(src)
	n := len(runes)
	i := 0
	for i < n {
		ch := runes[i]
		if unicode.IsSpace(ch) {
			i++
			continue
		}
		switch {
		case ch == '&':
			i++
			j := i
			for i < n && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i]) || runes[i] == '_') {
				i++
			}
			out = append(out, tok{tParam, string(runes[j:i])})
		case ch == '"':
			i++
			j := i
			for i < n && runes[i] != '"' {
				i++
			}
			out = append(out, tok{tStr, string(runes[j:i])})
			if i < n {
				i++
			}
		case ch == '\'':
			i++
			j := i
			for i < n && runes[i] != '\'' {
				i++
			}
			out = append(out, tok{tStr, string(runes[j:i])})
			if i < n {
				i++
			}
		case ch == '.':
			out = append(out, tok{tDot, "."})
			i++
		case ch == ',':
			out = append(out, tok{tComma, ","})
			i++
		case ch == '(':
			out = append(out, tok{tLParen, "("})
			i++
		case ch == ')':
			out = append(out, tok{tRParen, ")"})
			i++
		case ch == '*':
			out = append(out, tok{tStar, "*"})
			i++
		case ch == '<':
			if i+1 < n && runes[i+1] == '>' {
				out = append(out, tok{tOp, "<>"})
				i += 2
			} else if i+1 < n && runes[i+1] == '=' {
				out = append(out, tok{tOp, "<="})
				i += 2
			} else {
				out = append(out, tok{tOp, "<"})
				i++
			}
		case ch == '>':
			if i+1 < n && runes[i+1] == '=' {
				out = append(out, tok{tOp, ">="})
				i += 2
			} else {
				out = append(out, tok{tOp, ">"})
				i++
			}
		case ch == '!' && i+1 < n && runes[i+1] == '=':
			out = append(out, tok{tOp, "<>"})
			i += 2
		case ch == '=' || ch == '+' || ch == '-' || ch == '/':
			out = append(out, tok{tOp, string(ch)})
			i++
		case unicode.IsLetter(ch) || ch == '_':
			j := i
			for i < n && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i]) || runes[i] == '_') {
				i++
			}
			out = append(out, tok{tIdent, string(runes[j:i])})
		case unicode.IsDigit(ch):
			j := i
			for i < n && (unicode.IsDigit(runes[i]) || runes[i] == '.') {
				i++
			}
			out = append(out, tok{tNum, string(runes[j:i])})
		default:
			i++
		}
	}
	out = append(out, tok{tEOF, ""})
	return out
}

// --- source type mapping ---

// sourcePrefix maps uppercased 1C source type names to SQL table prefix.
// Empty prefix means the entity name is the table name as-is (lowercased).
var sourcePrefix = map[string]string{
	"РЕГИСТРНАКОПЛЕНИЯ":    "рег_",
	"ACCUMULATIONREGISTER": "рег_",
	"СПРАВОЧНИК":           "",
	"CATALOG":              "",
	"ДОКУМЕНТ":             "",
	"DOCUMENT":             "",
}

func isSourceType(upper string) bool {
	_, ok := sourcePrefix[upper]
	return ok
}

func sourceToTable(typeUpper, entityName string) string {
	return sourcePrefix[typeUpper] + strings.ToLower(entityName)
}

// --- keyword mapping ---

// kwMap maps Russian/English keywords to SQL equivalents.
// Aggregate functions are NOT included here — they are handled separately
// because they must only match when followed by "(".
var kwMap = map[string]string{
	// Russian structural keywords
	"ВЫБРАТЬ":       "SELECT",
	"РАЗЛИЧНЫЕ":     "DISTINCT",
	"ИЗ":            "FROM",
	"ГДЕ":           "WHERE",
	"СГРУППИРОВАТЬ": "GROUP",
	"УПОРЯДОЧИТЬ":   "ORDER",
	"ПО":            "BY",
	"ИМЕЯ":          "HAVING",
	"КАК":           "AS",
	"И":             "AND",
	"ИЛИ":           "OR",
	"НЕ":            "NOT",
	"ВЫБОР":         "CASE",
	"КОГДА":         "WHEN",
	"ТОГДА":         "THEN",
	"ИНАЧЕ":         "ELSE",
	"КОНЕЦ":         "END",
	"УБЫВ":          "DESC",
	"ВОЗР":          "ASC",
	"ЕСТЬ":          "IS",
	"ПУСТО":         "NULL",
	"В":             "IN",
	"ОБЪЕДИНИТЬ":    "UNION",
	"ВСЕ":           "ALL",
	// English pass-through
	"SELECT":   "SELECT",
	"DISTINCT": "DISTINCT",
	"FROM":     "FROM",
	"WHERE":    "WHERE",
	"GROUP":    "GROUP",
	"ORDER":    "ORDER",
	"BY":       "BY",
	"HAVING":   "HAVING",
	"AS":       "AS",
	"AND":      "AND",
	"OR":       "OR",
	"NOT":      "NOT",
	"CASE":     "CASE",
	"WHEN":     "WHEN",
	"THEN":     "THEN",
	"ELSE":     "ELSE",
	"END":      "END",
	"DESC":     "DESC",
	"ASC":      "ASC",
	"IS":       "IS",
	"NULL":     "NULL",
	"IN":       "IN",
	"UNION":    "UNION",
	"ALL":      "ALL",
}

// aggFuncs maps aggregate function names (only valid before "(").
var aggFuncs = map[string]string{
	"СУММА":      "SUM",
	"КОЛИЧЕСТВО": "COUNT",
	"МИНИМУМ":    "MIN",
	"МАКСИМУМ":   "MAX",
	"СРЕДНЕЕ":    "AVG",
	"SUM":        "SUM",
	"COUNT":      "COUNT",
	"MIN":        "MIN",
	"MAX":        "MAX",
	"AVG":        "AVG",
}

func sqlKW(ident string) (string, bool) {
	kw, ok := kwMap[strings.ToUpper(ident)]
	return kw, ok
}

func sqlAgg(ident string) (string, bool) {
	kw, ok := aggFuncs[strings.ToUpper(ident)]
	return kw, ok
}

// --- translator ---

type translator struct {
	tokens      []tok
	pos         int
	args        []any
	params      map[string]int // param name → 1-based position in args
	paramValues map[string]any
	parts       []string
}

func (tr *translator) peek(offset int) tok {
	i := tr.pos + offset
	if i >= len(tr.tokens) {
		return tok{tEOF, ""}
	}
	return tr.tokens[i]
}

func (tr *translator) advance() tok {
	t := tr.tokens[tr.pos]
	tr.pos++
	return t
}

func (tr *translator) emit(s string) {
	tr.parts = append(tr.parts, s)
}

func (tr *translator) build() string {
	var sb strings.Builder
	for i, p := range tr.parts {
		if i > 0 {
			prev := tr.parts[i-1]
			noBefore := p == "," || p == ")" || p == "." || p == "("
			noAfter := prev == "(" || prev == "."
			if !noBefore && !noAfter {
				sb.WriteByte(' ')
			}
		}
		sb.WriteString(p)
	}
	return sb.String()
}

func translate(tokens []tok, paramValues map[string]any) (Result, error) {
	if paramValues == nil {
		paramValues = map[string]any{}
	}
	tr := &translator{
		tokens:      tokens,
		params:      map[string]int{},
		paramValues: paramValues,
	}
	for {
		t := tr.peek(0)
		if t.kind == tEOF {
			break
		}
		upper := strings.ToUpper(t.val)

		// Source type: TypeName.EntityName → table_name
		if t.kind == tIdent && isSourceType(upper) &&
			tr.peek(1).kind == tDot && tr.peek(2).kind == tIdent {
			tr.advance()
			tr.advance()
			entity := tr.advance()
			tr.emit(sourceToTable(upper, entity.val))
			continue
		}

		// Multi-word: СГРУППИРОВАТЬ ПО / УПОРЯДОЧИТЬ ПО
		if t.kind == tIdent && (upper == "СГРУППИРОВАТЬ" || upper == "УПОРЯДОЧИТЬ") {
			tr.advance()
			kw := "GROUP BY"
			if upper == "УПОРЯДОЧИТЬ" {
				kw = "ORDER BY"
			}
			if tr.peek(0).kind == tIdent && strings.ToUpper(tr.peek(0).val) == "ПО" {
				tr.advance()
			}
			tr.emit(kw)
			continue
		}

		// Parameter: &Name → $N, or NULL literal when value is nil.
		// Emitting NULL avoids "could not determine data type of parameter $N"
		// errors in PostgreSQL when the parameter is only used in IS NULL checks.
		if t.kind == tParam {
			tr.advance()
			if _, exists := tr.params[t.val]; !exists {
				v := tr.paramValues[t.val]
				if v == nil {
					tr.params[t.val] = 0 // sentinel: emit NULL literal
				} else {
					tr.args = append(tr.args, v)
					tr.params[t.val] = len(tr.args)
				}
			}
			if tr.params[t.val] == 0 {
				tr.emit("NULL")
			} else {
				tr.emit(fmt.Sprintf("$%d%s", tr.params[t.val], pgCast(tr.paramValues[t.val])))
			}
			continue
		}

		// String literal
		if t.kind == tStr {
			tr.advance()
			tr.emit("'" + strings.ReplaceAll(t.val, "'", "''") + "'")
			continue
		}

		// Number / star / operator
		if t.kind == tNum || t.kind == tStar || t.kind == tOp {
			tr.advance()
			tr.emit(t.val)
			continue
		}

		// Punctuation (no extra spaces around , . parens)
		if t.kind == tComma || t.kind == tDot || t.kind == tLParen || t.kind == tRParen {
			tr.advance()
			tr.emit(t.val)
			continue
		}

		// Identifiers: aggregate function (only before "("), keyword, or lowercase field name
		if t.kind == tIdent {
			tr.advance()
			if agg, ok := sqlAgg(t.val); ok && tr.peek(0).kind == tLParen {
				tr.emit(agg)
			} else if kw, ok := sqlKW(t.val); ok {
				tr.emit(kw)
			} else {
				tr.emit(strings.ToLower(t.val))
			}
			continue
		}

		tr.advance()
	}
	return Result{SQL: tr.build(), Args: tr.args}, nil
}

// pgCast returns a PostgreSQL explicit cast suffix for v so that PostgreSQL
// can determine the parameter type even when context alone is insufficient.
func pgCast(v any) string {
	switch v.(type) {
	case time.Time:
		return "::timestamptz"
	case string:
		return "::text"
	case float64, float32:
		return "::numeric"
	case int, int32, int64, uint, uint32, uint64:
		return "::bigint"
	case bool:
		return "::boolean"
	}
	return ""
}
