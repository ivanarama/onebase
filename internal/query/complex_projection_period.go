package query

import "strings"

// Service dates belong to a particular source and SELECT. Adding Период to
// queriedColumnTypes would also assign a date to a same-named string column
// selected from a different source. Keep this proof local to the projection;
// derived aliases are covered by the inner SELECT that reads the actual date.
func complexProjectionRegisterPeriods(tokens []tok, opts CompileOpts) map[string]bool {
	ctx := preScanSourceContext(tokens)
	sources := indexColumnSources(tokens, opts, ctx)
	out := map[string]bool{}
	for i, token := range tokens {
		if token.kind != tIdent {
			continue
		}
		if kw, ok := sqlKW(token.val); !ok || kw != "SELECT" {
			continue
		}
		scopeID, ok := ctx.scopeIDAt(i)
		if !ok {
			continue
		}
		for _, item := range splitProjectionItems(tokens[i+1 : topLevelFrom(tokens, i)]) {
			col, _ := parseProjectionItem(item)
			if len(col.Path) < 1 || len(col.Path) > 2 {
				continue
			}
			name := lowerFast(col.Path[len(col.Path)-1])
			if name != "период" && name != "period" {
				continue
			}
			for _, source := range sources[scopeID] {
				if len(col.Path) == 1 && source.tokenPos != ctx.scopes[scopeID].mainSourcePos {
					continue
				}
				if len(col.Path) == 2 && !strings.EqualFold(col.Path[0], source.qualifier) {
					continue
				}
				if diagnosticSourceHasPeriod(tokens, source.tokenPos, opts) {
					out[name] = true
				}
			}
		}
	}
	return out
}

func diagnosticSourceHasPeriod(tokens []tok, pos int, opts CompileOpts) bool {
	if pos < 0 {
		return false // derived table, not a proved physical register
	}
	kind, name := upperFast(tokens[pos].val), tokens[pos+2].val
	if isInfoRegType(kind) {
		// Both slice generators export period only for periodic registers.
		for _, reg := range opts.InfoRegs {
			if strings.EqualFold(reg.Name, name) {
				return reg.Periodic
			}
		}
	}
	if isAccumRegType(kind) {
		for _, reg := range opts.Registers {
			if !strings.EqualFold(reg.Name, name) {
				continue
			}
			if pos+3 >= len(tokens) || tokens[pos+3].kind != tDot {
				return true
			}
			// Balances and ungrouped turnovers have no service date in their
			// output. Reuse the generator's argument parser and periodicity rule.
			if pos+5 < len(tokens) && tokens[pos+5].kind == tLParen &&
				accumVTKinds[upperFast(tokens[pos+4].val)] == "turnovers" {
				parser := translator{tokens: tokens, pos: pos + 6}
				args := parser.parseVTArgs()
				if len(args) > 2 {
					_, periodic := detectPeriodicity(args[2])
					return periodic
				}
			}
		}
	}
	return false // ordinary entity, nonperiodic source or excluded account register
}
