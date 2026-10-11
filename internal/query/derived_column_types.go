package query

import "github.com/ivantit66/onebase/internal/metadata"

// buildScalarColumnTypes propagates proven projection types from child SELECTs
// to their derived-table aliases before calendar functions are rewritten.
// Unknown expressions still reserve their output name, so a same-named date
// from another source cannot supply their type.
func buildScalarColumnTypes(tokens []tok, opts CompileOpts, ctx sourceContext) (
	map[int]map[string]metadata.FieldType,
	map[int]map[string]map[string]metadata.FieldType,
) {
	qualified := buildQualifiedColTypes(tokens, opts, ctx)
	scoped := buildScopedColTypes(tokens, opts, ctx, nil)
	hasDerived := false
	for _, scope := range ctx.scopes {
		hasDerived = hasDerived || len(scope.derivedAliases) > 0
	}
	if !hasDerived {
		return scoped, qualified
	}
	derived := map[int]map[string]map[string]metadata.FieldType{}
	outputs := map[int]map[string]metadata.FieldType{}
	var resolve func(int) map[string]metadata.FieldType
	resolve = func(scopeID int) map[string]metadata.FieldType {
		if fields, done := outputs[scopeID]; done {
			return fields
		}
		outputs[scopeID] = map[string]metadata.FieldType{}
		derived[scopeID] = map[string]map[string]metadata.FieldType{}
		if qualified[scopeID] == nil {
			qualified[scopeID] = map[string]map[string]metadata.FieldType{}
		}
		for alias, childID := range ctx.scopes[scopeID].derivedAliases {
			fields := resolve(childID)
			derived[scopeID][alias] = fields
			// An explicit derived alias shadows an implicit physical qualifier.
			qualified[scopeID][alias] = fields
		}
		if len(ctx.scopes[scopeID].derivedAliases) > 0 {
			scoped[scopeID] = buildScopedColTypes(tokens, opts, ctx, derived)[scopeID]
		}
		outputs[scopeID] = scalarProjectionTypes(tokens, ctx, scopeID, scoped[scopeID], qualified[scopeID])
		return outputs[scopeID]
	}
	for scopeID := range ctx.scopes {
		resolve(scopeID)
	}
	return scoped, qualified
}

func scalarProjectionTypes(tokens []tok, ctx sourceContext, scopeID int,
	fields map[string]metadata.FieldType, qualified map[string]map[string]metadata.FieldType,
) map[string]metadata.FieldType {
	output := map[string]metadata.FieldType{}
	start := -1
	for i, t := range tokens {
		if id, ok := ctx.scopeIDAt(i); ok && id == scopeID && t.kind == tIdent {
			if kw, ok := sqlKW(t.val); ok && kw == "SELECT" {
				start = i
				break
			}
		}
	}
	if start < 0 {
		return output
	}
	end := start + 1
	for end < len(tokens) && ctx.tokenSection[end] == sectionSelect && ctx.tokenScope[end] == scopeID {
		end++
	}
	// A UNION exports columns by position across several scopes. Its first
	// branch alone does not prove the type of the resulting column.
	union := false
	for i := end; i < len(tokens) && ctx.tokenDepth[i] >= ctx.tokenDepth[start]; i++ {
		if tokens[i].kind == tIdent && ctx.tokenDepth[i] == ctx.tokenDepth[start] {
			if kw, ok := sqlKW(tokens[i].val); ok && kw == "UNION" {
				union = true
			}
		}
	}
	for _, item := range splitProjectionItems(tokens[start+1 : end]) {
		column, _ := parseProjectionItem(item)
		if column.Output == "" || column.Star {
			continue
		}
		typ := metadata.FieldType("")
		if !union {
			expr := item
			if n := len(item); n >= 2 && item[n-2].kind == tIdent {
				if kw, ok := sqlKW(item[n-2].val); ok && kw == "AS" {
					expr = item[:n-2]
				}
			}
			typ = scalarProjectionExpressionType(expr, fields, qualified)
		}
		if _, duplicate := output[column.Output]; duplicate {
			typ = ""
		}
		output[column.Output] = typ
	}
	return output
}

// Only a direct field and MIN/MAX of a proven field preserve its type here.
// Calendar results, casts, strings and arbitrary expressions are not moments.
func scalarProjectionExpressionType(expr []tok, fields map[string]metadata.FieldType,
	qualified map[string]map[string]metadata.FieldType,
) metadata.FieldType {
	start, end := trimProjectionParentheses(expr, 0, len(expr))
	expr = expr[start:end]
	if len(expr) >= 4 && expr[0].kind == tIdent && expr[1].kind == tLParen && expr[len(expr)-1].kind == tRParen {
		if kw, ok := sqlAgg(expr[0].val); ok && (kw == "MIN" || kw == "MAX") {
			return scalarProjectionExpressionType(expr[2:len(expr)-1], fields, qualified)
		}
	}
	if len(expr) == 1 && expr[0].kind == tIdent {
		return fields[lowerFast(expr[0].val)]
	}
	if len(expr) == 3 && expr[0].kind == tIdent && expr[1].kind == tDot && expr[2].kind == tIdent {
		return qualified[lowerFast(expr[0].val)][lowerFast(expr[2].val)]
	}
	return ""
}
