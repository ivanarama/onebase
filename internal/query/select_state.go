package query

import "github.com/ivantit66/onebase/internal/metadata"

// The token scope, rather than parenthesis depth, owns reference navigation:
// sibling UNION branches and nested SELECTs may reuse names for different targets.
// Keep emission state too, so returning from a subquery restores its outer FROM.
type selectTranslationState struct {
	colMap      map[string]string
	colTypes    map[string]metadata.FieldType
	refDims     []refDimInfo
	mainTable   string
	mainRef     mainRefSource
	mainEmitted bool
	section     querySection
	aliases     map[string]struct{}
}

func (tr *translator) initSelectStates() {
	scopedTokens := make([][]tok, len(tr.sourceCtx.scopes))
	for i, token := range tr.tokens {
		if id, ok := tr.sourceCtx.scopeIDAt(i); ok {
			scopedTokens[id] = append(scopedTokens[id], token)
		}
	}
	tr.selectStates = make([]selectTranslationState, len(scopedTokens))
	allDims := make([][]refDimInfo, len(scopedTokens))
	for id, tokens := range scopedTokens {
		scope := tr.sourceCtx.scopes[id]
		allDims[id] = preScanAllRefDims(tokens, tr.opts)
		state := selectTranslationState{
			colMap:    buildColMap(tokens, tr.opts),
			colTypes:  buildColTypes(tokens, tr.opts),
			refDims:   filterUsedRefDims(allDims[id], tokens),
			mainTable: preScanMainTable(tokens),
			mainRef:   preScanMainRefSource(tokens, tr.opts),
			section:   sectionOther,
			aliases:   map[string]struct{}{},
		}
		// Direct qualified references also occur only inside correlated children.
		// Their physical columns do not require an auto-JOIN in the owner SELECT.
		for _, rd := range allDims[id] {
			state.colMap[rd.fieldName] = rd.idCol
		}
		if _, derived := scope.derivedAliases[scope.mainTable]; derived {
			// A derived FROM has no physical reference fields of its own.
			// An explicit JOIN after it must not become the main source.
			state.colMap = map[string]string{}
			state.colTypes = map[string]metadata.FieldType{}
			state.refDims = nil
			state.mainRef = mainRefSource{}
			state.mainTable = scope.mainTable
			state.mainEmitted = true
		}
		tr.selectStates[id] = state
	}
	// References used only by correlated children still need a JOIN in their
	// owning SELECT. Resolve the name through parents, never UNION siblings;
	// a child's own field or source qualifier hides an outer reference.
	for pos, token := range tr.tokens {
		if token.kind != tIdent {
			continue
		}
		// Bare references in every child expression clause need the owner's
		// reference metadata before its FROM is emitted. Projection, grouping
		// and ordering use the display; WHERE/HAVING keep the physical FK.
		// Output aliases and function names are declarations, not field reads.
		if pos+1 >= len(tr.tokens) || tr.tokens[pos+1].kind != tDot {
			section := tr.sourceCtx.sectionAt(pos)
			if (section != sectionSelect && section != sectionWhere &&
				section != sectionGroupBy && section != sectionHaving && section != sectionOrderBy) ||
				(pos+1 < len(tr.tokens) && tr.tokens[pos+1].kind == tLParen) {
				continue
			}
			if pos > 0 {
				if kw, ok := sqlKW(tr.tokens[pos-1].val); ok && kw == "AS" {
					continue
				}
			}
		}
		if pos > 0 && tr.tokens[pos-1].kind == tDot {
			continue // qualified fields keep their local prescan path
		}
		id, ok := tr.sourceCtx.scopeIDAt(pos)
		if !ok {
			continue
		}
		name := lowerFast(token.val)
		if pos+1 >= len(tr.tokens) || tr.tokens[pos+1].kind != tDot {
			scope := tr.sourceCtx.scopes[id]
			if tr.sourceCtx.isOutputAliasAt(pos, name) {
				continue // an output alias does not read an outer reference
			}
			if tr.sourceCtx.sectionAt(pos) == sectionOrderBy && scope.unionFirst != id {
				if _, output := tr.sourceCtx.scopes[scope.unionFirst].outputAliases[name]; output {
					continue // compound ORDER BY belongs to the first projection
				}
			}
			if _, derived := scope.derivedAliases[scope.mainTable]; scope.sourceCount > 0 && !derived {
				// Keep the existing bare-field path for a physical child FROM.
				// This compatibility fix concerns source-less/derived children.
				continue
			}
		}
		owner := tr.referenceOwner(id, name)
		if owner < 0 || owner == id {
			continue
		}
		state := &tr.selectStates[owner]
		for _, rd := range allDims[owner] {
			if rd.fieldName != name {
				continue
			}
			found := false
			for _, used := range state.refDims {
				if used.fieldName == name {
					found = true
					break
				}
			}
			if !found {
				state.refDims = append(state.refDims, rd)
			}
			break
		}
	}
}

// referenceIDColumn keeps a correlated predicate tied to the nearest owner,
// even when the child has a derived FROM with its own alias.
func (tr *translator) referenceIDColumn(rd *refDimInfo, name string) string {
	if id, ok := tr.sourceCtx.scopeIDAt(tr.pos - 1); ok {
		owner := tr.referenceOwner(id, name)
		if owner >= 0 && owner != id && tr.selectStates[owner].mainTable != "" {
			return tr.selectStates[owner].mainTable + "." + rd.idCol
		}
	}
	return tr.qualifyOwn(rd.idCol, name)
}

// referenceOwner returns the closest visible owner of an unqualified field.
// Source/derived aliases and non-reference fields also stop lookup: they must
// not be reinterpreted using an identically named reference in an outer SELECT.
func (tr *translator) referenceOwner(id int, name string) int {
	for id >= 0 {
		scope := tr.sourceCtx.scopes[id]
		if _, known := scope.qualifiers[name]; known {
			return -1
		}
		if _, known := scope.derivedAliases[name]; known {
			return -1
		}
		if _, own := tr.selectStates[id].colTypes[name]; own {
			return id
		}
		for _, entity := range scope.entities {
			if _, own := entity.fields[name]; own {
				return -1
			}
		}
		for _, child := range scope.derivedAliases {
			if tr.derivedProjectionShadows(child, name, map[int]bool{}) {
				return -1
			}
		}
		id = scope.parent
	}
	return -1
}

// derivedProjectionShadows permits correlation only when the derived output
// proves the name absent. UNION output names belong to its first SELECT; stars
// recurse into their actual sources rather than borrowing sibling metadata.
func (tr *translator) derivedProjectionShadows(id int, name string, seen map[int]bool) bool {
	id = tr.sourceCtx.scopes[id].unionFirst
	if seen[id] {
		return true // an unprovable output must not expose an outer reference
	}
	seen[id] = true
	defer delete(seen, id)
	start := -1
	depth := 0
	for i, token := range tr.tokens {
		if scope, ok := tr.sourceCtx.scopeIDAt(i); ok && scope == id && token.kind == tIdent {
			if kw, ok := sqlKW(token.val); ok && kw == "SELECT" {
				start, depth = i+1, tr.sourceCtx.tokenDepth[i]
				break
			}
		}
	}
	if start < 0 {
		return true
	}
	end := start
	for ; end < len(tr.tokens); end++ {
		if tr.sourceCtx.tokenDepth[end] < depth {
			break
		}
		if tr.sourceCtx.tokenDepth[end] == depth {
			scope, ok := tr.sourceCtx.scopeIDAt(end)
			if !ok || scope != id || tr.sourceCtx.sectionAt(end) != sectionSelect {
				break
			}
		}
	}
	for _, item := range splitProjectionItems(tr.tokens[start:end]) {
		column, _ := parseProjectionItem(item)
		if column.Output == name {
			return true
		}
		if !column.Star {
			if column.Output == "" {
				return true // an unnamed expression has a database-defined name
			}
			continue
		}
		scope := tr.sourceCtx.scopes[id]
		if len(item) == 3 && item[0].kind == tIdent && item[1].kind == tDot && item[2].kind == tStar {
			if tr.starSourceShadows(scope, lowerFast(item[0].val), name, seen) {
				return true
			}
			continue
		}
		if len(item) != 1 || item[0].kind != tStar || scope.sourceCount == 0 {
			return true
		}
		for qualifier := range scope.qualifiers {
			if tr.starSourceShadows(scope, qualifier, name, seen) {
				return true
			}
		}
		for qualifier := range scope.derivedAliases {
			if tr.starSourceShadows(scope, qualifier, name, seen) {
				return true
			}
		}
	}
	return false
}

func (tr *translator) starSourceShadows(scope sourceScope, qualifier, name string, seen map[int]bool) bool {
	if child, derived := scope.derivedAliases[qualifier]; derived {
		return tr.derivedProjectionShadows(child, name, seen)
	}
	if entity, known := scope.entities[qualifier]; known {
		_, own := entity.fields[name]
		_, system := entity.systemColumn(name)
		return own || system
	}
	// Registers, virtual tables and unknown metadata can have generated output
	// names. Without an exact expansion, absence is not proven.
	return true
}

func (tr *translator) activateSelect(id int) {
	if id == tr.activeSelect {
		return
	}
	if tr.activeSelect >= 0 {
		tr.selectStates[tr.activeSelect] = selectTranslationState{
			colMap: tr.colMap, colTypes: tr.colTypes, refDims: tr.refDims,
			mainTable: tr.mainTable, mainRef: tr.mainRef, mainEmitted: tr.mainEmitted,
			section: tr.section, aliases: tr.aliases,
		}
	}
	state := tr.selectStates[id]
	tr.colMap, tr.colTypes, tr.refDims = state.colMap, state.colTypes, state.refDims
	tr.mainTable, tr.mainRef, tr.mainEmitted = state.mainTable, state.mainRef, state.mainEmitted
	tr.section, tr.aliases = state.section, state.aliases
	tr.activeSelect = id
}

// qualifiedSourceColumn resolves a direct column by its source owner. Only parent
// SELECTs are visible; sibling UNION branches never supply a missing qualifier.
// A local source shadows its outer namesake even when it lacks the requested field.
func (tr *translator) qualifiedSourceColumn(pos int, name string) (string, metadata.FieldType, bool) {
	if pos < 2 || tr.tokens[pos-1].kind != tDot || tr.tokens[pos-2].kind != tIdent {
		return "", "", false
	}
	qualifier := lowerFast(tr.tokens[pos-2].val)
	id, ok := tr.sourceCtx.scopeIDAt(pos)
	if !ok {
		return "", "", false
	}
	for id >= 0 {
		scope := tr.sourceCtx.scopes[id]
		if _, derived := scope.derivedAliases[qualifier]; derived {
			return "", "", false
		}
		if _, known := scope.qualifiers[qualifier]; known {
			// Local columns keep the existing comparison/coalesce path.
			if id == tr.activeSelect || qualifier != scope.mainTable {
				return "", "", false
			}
			state := tr.selectStates[id]
			typ, known := state.colTypes[name]
			if !known {
				return "", "", false
			}
			col := name
			if mapped, found := state.colMap[name]; found {
				col = mapped
			}
			return col, typ, true
		}
		id = scope.parent
	}
	return "", "", false
}

// The compound ORDER BY uses the first projection's aliases, never the last
// branch's source columns or reference auto-JOINs.
func (tr *translator) unionOutputAlias(name string) bool {
	if tr.section != sectionOrderBy || !tr.unionOrders[tr.parenDepth] {
		return false
	}
	scope, ok := tr.sourceCtx.scopeAt(tr.pos - 1)
	if !ok {
		return false
	}
	_, found := tr.selectStates[scope.unionFirst].aliases[name]
	return found
}
