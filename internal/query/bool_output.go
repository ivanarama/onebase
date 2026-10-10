package query

import "github.com/ivantit66/onebase/internal/metadata"

// scopedBoolOutputColumns proves each direct output field against its own
// SELECT sources. A nested filter does not invalidate the outer projection;
// derived tables export only proven types, while UNION and expressions remain
// untyped. Normalization uses the actual SQL key, preserving explicit aliases.
func scopedBoolOutputColumns(tokens []tok, ctx sourceContext,
	scoped map[int]map[string]metadata.FieldType,
	qualified map[int]map[string]map[string]metadata.FieldType,
) []string {
	if len(ctx.scopes) == 0 {
		return nil
	}
	start := -1
	for i, t := range tokens {
		if t.kind == tIdent {
			if kw, ok := sqlKW(t.val); ok && kw == "SELECT" {
				start = i
				break
			}
		}
	}
	if start < 0 {
		return nil
	}
	scopeID, ok := ctx.scopeIDAt(start)
	if !ok {
		return nil
	}
	end := topLevelFrom(tokens, start)
	// UNION types must agree by position across all branches; a direct field
	// in the first SELECT alone cannot prove the result type.
	for i := end; i < len(tokens) && ctx.tokenDepth[i] >= ctx.tokenDepth[start]; i++ {
		if tokens[i].kind == tIdent && ctx.tokenDepth[i] == ctx.tokenDepth[start] {
			if kw, ok := sqlKW(tokens[i].val); ok && kw == "UNION" {
				return nil
			}
		}
	}
	var candidates []string
	counts := map[string]int{}
	add := func(from, to int) {
		item := trimProjectionItem(tokens[from:to])
		from = to - len(item)
		col, _ := parseProjectionItem(item)
		if col.Output == "" {
			return
		}
		fieldEnd := from + 2*len(col.Path) - 1
		output := projectionSQLKey(tokens, ctx, col, fieldEnd)
		counts[output]++
		// Prove the expression independently: two logical names can emit
		// distinct SQL keys and must not erase each other's source types.
		if len(col.Path) > 0 {
			if scalarProjectionFieldTypeAt(tokens, ctx, from, fieldEnd, scoped[scopeID], qualified[scopeID]) == metadata.FieldTypeBool {
				candidates = append(candidates, output)
			}
		}
	}
	depth, from := 0, start+1
	for i := from; i < end; i++ {
		switch tokens[i].kind {
		case tLParen:
			depth++
		case tRParen:
			depth--
		case tComma:
			if depth == 0 {
				add(from, i)
				from = i + 1
			}
		}
	}
	add(from, end)
	var out []string
	for _, output := range candidates {
		// Different DSL names can emit the same SQL key (Проведен ->
		// posted). A colliding string or expression must not become bool.
		if counts[output] == 1 {
			out = append(out, output)
		}
	}
	return out
}
