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
