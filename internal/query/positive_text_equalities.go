package query

// positiveTextEqualities recognises a deliberately small WHERE grammar:
// comparisons of simple operands, boolean parentheses, AND, OR and NOT.
// Replacing FALSE by UNKNOWN preserves row inclusion through positive AND/OR,
// but not through NOT or a scalar expression. Unknown grammar keeps COALESCE
// for the whole filter. Each SELECT is inspected independently; predicates in
// projection, HAVING, JOIN and virtual-table arguments are never marked.
func positiveTextEqualities(tokens []tok, ctx sourceContext) map[int]bool {
	out := make(map[int]bool)
	for i, token := range tokens {
		if token.kind != tIdent {
			continue
		}
		kw, ok := sqlKW(token.val)
		if !ok || kw != "WHERE" || ctx.sectionAt(i) != sectionWhere {
			continue
		}
		scope, ok := ctx.scopeIDAt(i)
		if !ok {
			continue
		}
		depth := ctx.tokenDepth[i]
		end := i + 1
		for end < len(tokens) && tokens[end].kind != tEOF {
			if tokens[end].kind == tIdent && ctx.tokenDepth[end] == depth {
				if kw, ok := sqlKW(tokens[end].val); ok && kw == "UNION" {
					break
				}
			}
			if ctx.tokenDepth[end] < depth ||
				(tokens[end].kind == tRParen && ctx.tokenDepth[end] == depth) {
				break
			}
			if ctx.tokenDepth[end] == depth &&
				(ctx.tokenScope[end] != scope || ctx.sectionAt(end) != sectionWhere) {
				break
			}
			end++
		}
		positions, valid := positiveComparisonTree(tokens, i+1, end)
		if valid {
			for _, pos := range positions {
				out[pos] = true
			}
		}
	}
	return out
}

func positiveComparisonTree(tokens []tok, start, end int) ([]int, bool) {
	start, end = trimProjectionParentheses(tokens, start, end)
	if start >= end {
		return nil, false
	}
	// Split by precedence, respecting parentheses. A scalar CASE/BETWEEN or
	// subquery cannot pass the simple-leaf grammar, so its entire filter falls
	// back rather than interpreting an internal AND as a boolean tree edge.
	for _, operator := range []string{"OR", "AND"} {
		depth, part := 0, start
		var positions []int
		for i := start; i < end; i++ {
			switch tokens[i].kind {
			case tLParen:
				depth++
			case tRParen:
				depth--
				if depth < 0 {
					return nil, false
				}
			case tIdent:
				kw, ok := sqlKW(tokens[i].val)
				if depth == 0 && ok && kw == operator {
					p, valid := positiveComparisonTree(tokens, part, i)
					if !valid {
						return nil, false
					}
					positions = append(positions, p...)
					part = i + 1
				}
			}
		}
		if depth != 0 {
			return nil, false
		}
		if part != start {
			p, valid := positiveComparisonTree(tokens, part, end)
			return append(positions, p...), valid
		}
	}
	if tokens[start].kind == tIdent {
		if kw, ok := sqlKW(tokens[start].val); ok && kw == "NOT" {
			_, valid := positiveComparisonTree(tokens, start+1, end)
			return nil, valid // even double negation keeps the conservative path
		}
	}
	for i := start + 1; i < end-1; i++ {
		if tokens[i].kind != tOp {
			continue
		}
		switch tokens[i].val {
		case "=", "<>", "!=", "<", "<=", ">", ">=":
		default:
			return nil, false
		}
		left, leftOK := simpleComparisonOperand(tokens, start, i)
		right, rightOK := simpleComparisonOperand(tokens, i+1, end)
		if !leftOK || !rightOK {
			return nil, false
		}
		if tokens[i].val == "=" {
			if left >= 0 && end == i+2 && tokens[i+1].kind == tStr && tokens[i+1].val != "" {
				return []int{left}, true
			}
			if right >= 0 && i == start+1 && tokens[start].kind == tStr && tokens[start].val != "" {
				return []int{right}, true
			}
		}
		return nil, true
	}
	return nil, false
}

// Return the column token position, or -1 for a literal/parameter.
func simpleComparisonOperand(tokens []tok, start, end int) (int, bool) {
	if end-start == 1 {
		switch tokens[start].kind {
		case tStr, tNum, tParam:
			return -1, true
		case tIdent:
			if kw, ok := sqlKW(tokens[start].val); ok {
				return -1, kw == "TRUE" || kw == "FALSE" || kw == "NULL"
			}
			return start, true
		}
	}
	if end-start == 3 && tokens[start].kind == tIdent &&
		tokens[start+1].kind == tDot && tokens[start+2].kind == tIdent {
		return start + 2, true
	}
	return -1, false
}
