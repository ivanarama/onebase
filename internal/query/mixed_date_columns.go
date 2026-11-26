package query

import "github.com/ivantit66/onebase/internal/metadata"

// Object dates and register dates have different SQLite text encodings.
// Only rewrite a comparison if both operands are direct, typed columns.
func (tr *translator) sqliteMixedDateColumns() map[int]bool {
	var columns map[int]bool
	for op := range tr.tokens {
		if !tr.comparisonOpAt(op) {
			continue
		}
		left, leftClass := tr.dateComparisonOperand(op, -1)
		right, rightClass := tr.dateComparisonOperand(op, 1)
		if leftClass == sourceClassUnknown || rightClass == sourceClassUnknown || leftClass == rightClass {
			continue
		}
		if columns == nil {
			columns = make(map[int]bool)
		}
		columns[left], columns[right] = true, true
	}
	return columns
}

// Walk outwards from the operator. Accept a bare or qualified column with
// optional enclosing parentheses. An arithmetic expression, dereference or
// function call must keep its own result type and encoding.
func (tr *translator) dateComparisonOperand(op, step int) (int, sourceClass) {
	open, close := tLParen, tRParen
	if step < 0 {
		open, close = close, open
	}
	kindAt := func(i int) tokKind {
		if i < 0 || i >= len(tr.tokens) {
			return tEOF
		}
		return tr.tokens[i].kind
	}
	i := op + step
	parens := 0
	for kindAt(i) == open {
		parens++
		i += step
	}
	if kindAt(i) != tIdent {
		return -1, sourceClassUnknown
	}
	column := i
	i += step
	if kindAt(i) == tDot && kindAt(i+step) == tIdent {
		i += step
		if step > 0 {
			column = i
		}
		i += step
	}
	for ; parens > 0; parens-- {
		if kindAt(i) != close {
			return -1, sourceClassUnknown
		}
		i += step
	}
	switch kindAt(i) {
	case tIdent:
		kw, _ := sqlKW(tr.tokens[i].val)
		switch kw {
		case "SELECT", "ALL", "DISTINCT", "WHERE", "HAVING", "ON", "NOT", "AND", "OR", "WHEN", "THEN", "ELSE", "END",
			"AS", "FROM", "GROUP", "ORDER", "BY", "ASC", "DESC", "LIMIT", "UNION", "INNER", "LEFT", "RIGHT", "FULL", "CROSS", "JOIN":
		default:
			return -1, sourceClassUnknown
		}
	case close, tComma, tEOF:
	default:
		return -1, sourceClassUnknown
	}
	name := lowerFast(tr.tokens[column].val)
	qualifier := tr.columnQualifierAt(column)
	if qualifier == "" && tr.sourceCtx.isOutputAliasAt(column, name) {
		return -1, sourceClassUnknown
	}
	typ, source, ok := tr.columnSourceAt(column, name, qualifier)
	if !ok || typ != metadata.FieldTypeDate || source.tokenPos < 0 {
		return -1, sourceClassUnknown
	}
	kind := upperFast(tr.tokens[source.tokenPos].val)
	switch {
	case isEntitySourceType(kind):
		return column, sourceClassEntity
	case isInfoRegType(kind), isAccumRegType(kind), isAccountRegType(kind):
		return column, sourceClassRegister
	}
	return -1, sourceClassUnknown
}
