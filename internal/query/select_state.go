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
	for id, tokens := range scopedTokens {
		scope := tr.sourceCtx.scopes[id]
		state := selectTranslationState{
			colMap:    buildColMap(tokens, tr.opts),
			colTypes:  buildColTypes(tokens, tr.opts),
			refDims:   preScanRefDims(tokens, tr.opts),
			mainTable: preScanMainTable(tokens),
			mainRef:   preScanMainRefSource(tokens, tr.opts),
			section:   sectionOther,
			aliases:   map[string]struct{}{},
		}
		// Direct qualified references also occur only inside correlated children.
		// Their physical columns do not require an auto-JOIN in the owner SELECT.
		for _, rd := range preScanAllRefDims(tokens, tr.opts) {
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
