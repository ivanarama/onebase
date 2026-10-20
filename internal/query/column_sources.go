package query

import (
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Each entry represents one FROM source, not one qualifier spelling. Repeated
// sources remain distinct even when they have identical metadata. A source with
// unknown columns prevents inference of unqualified names from another source.
type columnSource struct {
	tokenPos  int // source identity is independent of its SQL qualifier
	qualifier string
	fields    map[string]metadata.FieldType
}

func indexColumnSources(tokens []tok, opts CompileOpts, ctx sourceContext) map[int][]columnSource {
	out := map[int][]columnSource{}
	for i := 0; i+2 < len(tokens); i++ {
		if ctx.sectionAt(i) != sectionFrom || tokens[i].kind != tIdent ||
			!isSourceType(upperFast(tokens[i].val)) || tokens[i+1].kind != tDot || tokens[i+2].kind != tIdent {
			continue
		}
		scopeID, ok := ctx.scopeIDAt(i)
		if !ok {
			continue
		}
		kind, name := upperFast(tokens[i].val), tokens[i+2].val
		source := columnSource{tokenPos: i, qualifier: sourceToTable(kind, name), fields: sourceColumnTypes(kind, name, opts)}
		if sourceColTypes(kind, name, opts) == nil {
			source.fields = nil
		}
		reserveStandardSourceNames(source.fields, kind)
		end := i + 3
		if end+1 < len(tokens) && tokens[end].kind == tDot {
			// A dotted path is not the parent entity. Only supported virtual
			// tables supply types here; other paths keep the existing emission.
			source.fields = nil
			if end+2 < len(tokens) && tokens[end+2].kind == tLParen {
				vt := upperFast(tokens[end+1].val)
				_, accum := accumVTKinds[vt]
				_, info := infoVTKinds[vt]
				if (accum && (isAccumRegType(kind) || isAccountRegType(kind))) || (info && isInfoRegType(kind)) {
					source.fields = virtualColumnTypes(kind, name, vt, opts)
				}
				depth := 1
				end += 3
				for end < len(tokens) && depth > 0 {
					switch tokens[end].kind {
					case tLParen:
						depth++
					case tRParen:
						depth--
					}
					end++
				}
				// No public qualifier without AS: generated VT aliases are not
				// sourceToTable(kind,name). Bare columns can still be resolved.
				source.qualifier = ""
			} else {
				end += 2
			}
		}
		if end+1 < len(tokens) && tokens[end].kind == tIdent {
			if kw, ok := sqlKW(tokens[end].val); ok && kw == "AS" && tokens[end+1].kind == tIdent {
				source.qualifier = lowerFast(tokens[end+1].val)
			}
		}
		out[scopeID] = append(out[scopeID], source)
	}
	for scopeID, scope := range ctx.scopes {
		for alias := range scope.derivedAliases {
			// Propagating derived expression types is a separate contract.
			// Reserve the source so it cannot borrow a same-named physical type.
			out[scopeID] = append(out[scopeID], columnSource{tokenPos: -1, qualifier: alias})
		}
	}
	return out
}

func virtualColumnTypes(kind, name, vt string, opts CompileOpts) map[string]metadata.FieldType {
	fields := sourceColumnTypes(kind, name, opts)
	if isAccumRegType(kind) {
		for _, reg := range opts.Registers {
			if !strings.EqualFold(reg.Name, name) {
				continue
			}
			// Use the columns actually exported by this VT, including MIN
			// attributes in all three accumulation VTs and synthetic resource names.
			fields = map[string]metadata.FieldType{}
			for _, dim := range reg.Dimensions {
				fields[lowerFast(dim.Name)] = dim.Type
			}
			for _, attr := range reg.Attributes {
				fields[lowerFast(attr.Name)] = attr.Type
			}
			for _, resource := range reg.Resources {
				base := lowerFast(resource.Name)
				switch accumVTKinds[vt] {
				case "balances":
					fields[base+"остаток"] = resource.Type
				case "balances_turnovers":
					fields[base+"начальный"] = resource.Type
					fields[base+"конечный"] = resource.Type
					fields[base+"приход"] = resource.Type
					fields[base+"расход"] = resource.Type
				case "turnovers":
					fields[base+"приход"] = resource.Type
					fields[base+"расход"] = resource.Type
					fields[base+"оборот"] = resource.Type
				}
			}
		}
	}
	if isAccountRegType(kind) {
		for _, reg := range opts.AccountRegs {
			if !strings.EqualFold(reg.Name, name) {
				continue
			}
			for _, resource := range reg.Resources {
				base := lowerFast(resource.Name)
				delete(fields, base)
				fields[base+"_дт"] = resource.Type
				fields[base+"_кт"] = resource.Type
				if accumVTKinds[vt] == "balances" {
					fields[base+"остаток"] = resource.Type
				}
			}
		}
	}
	return fields
}

func (tr *translator) columnQualifierAt(idx int) string {
	if idx >= 2 && tr.tokens[idx-1].kind == tDot && tr.tokens[idx-2].kind == tIdent {
		return lowerFast(tr.tokens[idx-2].val)
	}
	return ""
}

func (tr *translator) columnTypeAt(idx int, name, qualifier string) (metadata.FieldType, string, bool) {
	typ, source, known := tr.columnSourceAt(idx, name, qualifier)
	return typ, source.qualifier, known
}

func (tr *translator) columnSourceAt(idx int, name, qualifier string) (metadata.FieldType, columnSource, bool) {
	// The identifier before a dot qualifies the following column. It must not
	// borrow a same-named field's type for CAST or automatic qualification.
	if idx+1 < len(tr.tokens) && tr.tokens[idx+1].kind == tDot {
		return "", columnSource{}, false
	}
	scopeID, ok := tr.sourceCtx.scopeIDAt(idx)
	if !ok {
		return "", columnSource{}, false
	}
	for scopeID >= 0 {
		count := 0
		var typ metadata.FieldType
		var owner columnSource
		for _, source := range tr.columnSources[scopeID] {
			if qualifier != "" {
				if !strings.EqualFold(qualifier, source.qualifier) {
					continue
				}
				count++ // existence shadows outer scopes, even with unknown type
				typ, owner = source.fields[name], source
			} else {
				if source.fields == nil {
					return "", columnSource{}, false
				}
				if t, exists := source.fields[name]; exists {
					count++
					typ, owner = t, source
				}
			}
		}
		if count > 0 {
			return typ, owner, count == 1 && typ != ""
		}
		if qualifier == "" {
			break // never infer a bare name from a foreign SELECT
		}
		scopeID = tr.sourceCtx.scopes[scopeID].parent
	}
	return "", columnSource{}, false
}

// Reserve standard names without adding new scalar rules for them. Existing
// explicit fields retain their metadata type; implicit standard fields must
// not borrow a custom type from another source or expand the main-source rule.
func reserveStandardSourceNames(fields map[string]metadata.FieldType, kind string) {
	if fields == nil {
		return
	}
	reserve := func(names ...string) {
		for _, name := range names {
			if _, exists := fields[name]; !exists {
				fields[name] = ""
			}
		}
	}
	if isAccumRegType(kind) || isInfoRegType(kind) || isAccountRegType(kind) {
		reserve("период", "period")
		return
	}
	reserve("ссылка", "reference", "ref", "id", "пометкаудаления", "deletion_mark")
	if kind == "ДОКУМЕНТ" || kind == "DOCUMENT" {
		reserve("номер", "number", "дата", "date", "проведен", "проведён", "posted")
	} else {
		reserve("код", "code", "наименование", "description")
	}
}
