package query

import "github.com/ivantit66/onebase/internal/metadata"

// scopedBoolOutputColumns proves each direct output field against its own
// SELECT sources. A nested filter does not invalidate the outer projection;
// derived tables export only proven types, while UNION and expressions remain
// untyped. Normalization uses the actual SQL key, preserving explicit aliases.
func scopedBoolOutputColumns(tokens []tok, opts CompileOpts, ctx sourceContext,
	scoped map[int]map[string]metadata.FieldType,
	qualified map[int]map[string]map[string]metadata.FieldType,
	keys *projectionKeyResolver,
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
	unknownOutput := false
	add := func(from, to int) {
		item := tokens[from:to]
		col, _ := parseProjectionItem(item)
		if col.Star {
			star := keys.starKeys(scopeID, item)
			unknownOutput = unknownOutput || !star.complete
			for key, count := range star.counts {
				counts[key] += count
			}
		}
		output, known := keys.outputSQLKey(col, from, to)
		unknownOutput = unknownOutput || (!col.Star && !known)
		if output == "" {
			return
		}
		fieldEnd := from + 2*len(col.Path) - 1
		counts[metadata.SQLIdent(output)]++
		// Prove the expression independently: two logical names can emit
		// distinct SQL keys and must not erase each other's source types.
		if len(col.Path) > 0 {
			typ := scalarProjectionFieldTypeAt(tokens, ctx, from, fieldEnd, scoped[scopeID], qualified[scopeID])
			if typ == "" {
				typ = referenceProjectionFieldType(tokens, opts, ctx, scopeID, col.Path)
			}
			if typ == metadata.FieldTypeBool {
				candidates = append(candidates, output)
			}
		}
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
	if unknownOutput {
		return nil
	}
	var out []string
	for _, output := range candidates {
		// Different DSL names can emit the same SQL key (Проведен ->
		// posted, or a long alias -> SQLIdent). A colliding string or
		// expression must not become bool.
		if counts[metadata.SQLIdent(output)] == 1 {
			out = append(out, output)
		}
	}
	return out
}

// Prove one reference hop against the main source whose auto-JOIN is emitted
// by the translator. A qualifier of an explicit/derived source takes priority
// over a reference name. Unknown qualifiers and further hops stay untyped.
func referenceProjectionFieldType(tokens []tok, opts CompileOpts, ctx sourceContext, scopeID int, path []string) metadata.FieldType {
	if len(path) != 2 && len(path) != 3 {
		return ""
	}
	scope := ctx.scopes[scopeID]
	if _, derived := scope.derivedAliases[scope.mainTable]; derived {
		return ""
	}
	if len(path) == 2 {
		qualifier := lowerFast(path[0])
		if _, source := scope.qualifiers[qualifier]; source {
			return ""
		}
		if _, derived := scope.derivedAliases[qualifier]; derived {
			return ""
		}
	}
	for i := 0; i+2 < len(tokens); i++ {
		id, ok := ctx.scopeIDAt(i)
		if !ok || id != scopeID || tokens[i].kind != tIdent || !isSourceType(upperFast(tokens[i].val)) ||
			tokens[i+1].kind != tDot || tokens[i+2].kind != tIdent {
			continue
		}
		if len(path) == 3 {
			qualifier := lowerFast(path[0])
			if qualifier != scope.mainTable && qualifier != lowerFast(tokens[i+2].val) &&
				qualifier != sourceToTable(upperFast(tokens[i].val), tokens[i+2].val) {
				return ""
			}
		}
		ref, attr := lowerFast(path[len(path)-2]), lowerFast(path[len(path)-1])
		if isReferenceName(attr) {
			// Ссылка/Reference/Ref emit the target UUID, even if metadata has
			// a same-named own bool field. Its declared type cannot prove id.
			return ""
		}
		for _, rd := range preScanAllRefDims(tokens[i:], opts) {
			if rd.fieldName != ref {
				continue
			}
			if target, found := scopeEntityInfo(rd.refEntity, opts); found {
				if typ, own := target.fields[attr]; own {
					return typ
				}
				if _, system := target.systemColumn(attr); system {
					return metadata.FieldTypeBool
				}
			}
			return ""
		}
		return ""
	}
	return ""
}
