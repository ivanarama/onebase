package query

import "github.com/ivantit66/onebase/internal/metadata"

// buildScalarColumnTypes propagates proven projection types from child SELECTs
// to their derived-table aliases before calendar functions are rewritten.
// Unknown expressions still reserve their output name, so a same-named date
// from another source cannot supply their type.
func buildScalarColumnTypes(tokens []tok, opts CompileOpts, ctx sourceContext, keys *projectionKeyResolver) (
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
		outputs[scopeID] = scalarProjectionTypes(tokens, ctx, scopeID, scoped[scopeID], qualified[scopeID], keys)
		return outputs[scopeID]
	}
	for scopeID := range ctx.scopes {
		resolve(scopeID)
	}
	return scoped, qualified
}

func scalarProjectionTypes(tokens []tok, ctx sourceContext, scopeID int,
	fields map[string]metadata.FieldType, qualified map[string]map[string]metadata.FieldType,
	keys *projectionKeyResolver,
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
	stars := projectionKeySet{counts: map[string]int{}, complete: true}
	complete := true
	physicalCounts := map[string]int{}
	add := func(from, to int) {
		item := tokens[from:to]
		column, _ := parseProjectionItem(item)
		if column.Star {
			stars.add(keys.starKeys(scopeID, item))
		}
		key, known := keys.outputSQLKey(column, from, to)
		complete = complete && (column.Star || known)
		if key == "" || column.Star {
			return
		}
		physicalCounts[metadata.SQLIdent(key)]++
		typ := metadata.FieldType("")
		if !union {
			expr := item
			if n := len(item); n >= 2 && item[n-2].kind == tIdent {
				if kw, ok := sqlKW(item[n-2].val); ok && kw == "AS" {
					expr = item[:n-2]
				}
			}
			typ = scalarProjectionFieldTypeAt(tokens, ctx, from, from+len(expr), fields, qualified)
			if column.Output == "" {
				start, end := trimProjectionParentheses(tokens, from, to)
				direct, _ := parseProjectionItem(tokens[start:end])
				if len(direct.Path) == 0 {
					// An implicit function name only reserves a collision;
					// it does not extend scalar type inference.
					typ = ""
				}
			}
		}
		if _, duplicate := output[key]; duplicate {
			typ = ""
		}
		output[key] = typ
	}
	depth, from := 0, selectProjectionStart(tokens, start)
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
	// Only proven absence from every projection and wildcard preserves a type.
	// This is independent of projection order and of the wildcard values.
	for key := range output {
		physical := metadata.SQLIdent(key)
		if !complete || !stars.complete || physicalCounts[physical] != 1 || stars.counts[physical] > 0 {
			output[key] = ""
		}
	}
	// Derived consumers can spell a long output either logically or with its
	// emitted SQLIdent. Export both only after every physical collision has
	// invalidated the type; a third occurrence must not restore it.
	physical := map[string]metadata.FieldType{}
	for key, typ := range output {
		physical[metadata.SQLIdent(key)] = typ
	}
	for key, typ := range physical {
		output[key] = typ
	}
	return output
}

// outputSQLKey reserves names independently of expression types. The boolean
// proves that no unaccounted identifier key can collide with a typed output.
// Keep this separate from the masking parser, where an expression still
// requires fail-closed field access.
func (r *projectionKeyResolver) outputSQLKey(column ProjectionColumn, from, to int) (string, bool) {
	tokens, ctx := r.tokens, r.ctx
	if column.Output == "" {
		start, end := trimProjectionParentheses(tokens, from, to)
		column, _ = parseProjectionItem(tokens[start:end])
		if len(column.Path) == 0 || column.Alias != "" {
			// Only a complete call can inherit a function's SQL output name.
			// Arithmetic, casts and other unknown expressions cannot prove absence
			// of a collision merely because their name was not resolved.
			if end-start >= 4 && tokens[start].kind == tIdent && tokens[start+1].kind == tLParen && tokens[end-1].kind == tRParen {
				depth := 0
				for i := start + 1; i < end; i++ {
					switch tokens[i].kind {
					case tLParen:
						depth++
					case tRParen:
						depth--
					}
					if depth == 0 {
						if i == end-1 {
							fn, known := sqlAgg(tokens[start].val)
							if !known {
								if r.scalarRewrites == nil {
									r.scalarRewrites = scalarFuncRewrites(dialectOrDefault(r.opts.Dialect).Name())
								}
								if rw, ok := r.scalarRewrites[lowerFast(tokens[start].val)]; ok &&
									len(rw.prefix) == 2 && rw.prefix[0].kind == tIdent && rw.prefix[1].kind == tLParen &&
									len(rw.suffix) == 1 && rw.suffix[0].kind == tRParen {
									fn, known = rw.prefix[0].val, true
								}
							}
							if known {
								if dialectOrDefault(r.opts.Dialect).Name() == "postgres" {
									return lowerFast(fn), true
								}
								// SQLite keeps the emitted call text (and wrapping parentheses),
								// which cannot equal a direct identifier or an explicit DSL alias.
								return "", true
							}
						}
						break
					}
				}
			}
			return "", false
		}
		from = start
	}
	return projectionSQLKey(tokens, ctx, column, from+2*len(column.Path)-1), true
}

// projectionSQLKey uses the names emitted by the SQL translator. Reference
// names, including explicit aliases, emit id; other explicit aliases remain
// logical here so result normalization can follow RestoreLongLabels. Uniqueness
// checks must apply metadata.SQLIdent before comparing these names.
func projectionSQLKey(tokens []tok, ctx sourceContext, column ProjectionColumn, fieldEnd int) string {
	if isReferenceName(column.Output) {
		return "id"
	}
	if column.Alias == "" && len(column.Path) > 0 && ctx.systemColumnIdentifierAt(tokens, fieldEnd-1, map[int]bool{}) {
		if physical, _, system := entitySystemColAlias(column.Output); system {
			return physical
		}
		if physical, system := systemColAlias(column.Output); system {
			return physical
		}
	}
	return column.Output
}

// scalarProjectionFieldTypeAt resolves direct system fields by their actual
// input SQL key too: a derived table exports posted, even when its child SELECT
// used Проведен. A non-reserved logical alias and a same-named own field retain
// their literal keys because the source context does not rewrite them.
func scalarProjectionFieldTypeAt(tokens []tok, ctx sourceContext, from, to int,
	fields map[string]metadata.FieldType, qualified map[string]map[string]metadata.FieldType,
) metadata.FieldType {
	start, end := trimProjectionParentheses(tokens, from, to)
	expr := tokens[start:end]
	if (len(expr) == 1 || len(expr) == 3 && expr[1].kind == tDot) &&
		ctx.systemColumnIdentifierAt(tokens, end-1, map[int]bool{}) {
		if physical, _, system := entitySystemColAlias(expr[len(expr)-1].val); system {
			if len(expr) == 3 {
				return qualified[lowerFast(expr[0].val)][physical]
			}
			if scopeID, ok := ctx.scopeIDAt(start); ok {
				return qualified[ctx.scopes[scopeID].mainTable][physical]
			}
		}
	}
	return scalarProjectionExpressionType(expr, fields, qualified)
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
		return fields[projectionInputSQLKey(expr[0].val)]
	}
	if len(expr) == 3 && expr[0].kind == tIdent && expr[1].kind == tDot && expr[2].kind == tIdent {
		return qualified[lowerFast(expr[0].val)][projectionInputSQLKey(expr[2].val)]
	}
	return ""
}

// References to a reserved alias read id too. Looking up its logical spelling
// would lose a proven derived type or miss the collision recorded under id.
func projectionInputSQLKey(name string) string {
	key := lowerFast(name)
	if isReferenceName(key) {
		return "id"
	}
	return key
}
