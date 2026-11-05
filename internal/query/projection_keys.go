package query

import (
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// projectionKeySet counts physical SQLIdent output names, independently of types.
// Incomplete expansions cannot prove that an explicit alias is collision-free.
// Counts retain separate source occurrences and repeated projection items.
type projectionKeySet struct {
	counts   map[string]int
	complete bool
}

func (s *projectionKeySet) add(other projectionKeySet) {
	s.complete = s.complete && other.complete
	for key, count := range other.counts {
		s.counts[key] += count
	}
}

type projectionKeyResolver struct {
	tokens         []tok
	opts           CompileOpts
	ctx            sourceContext
	outputs        map[int]projectionKeySet
	scalarRewrites map[string]funcRewrite
}

func newProjectionKeyResolver(tokens []tok, opts CompileOpts, ctx sourceContext) *projectionKeyResolver {
	return &projectionKeyResolver{tokens: tokens, opts: opts, ctx: ctx, outputs: map[int]projectionKeySet{}}
}

// starKeys expands only exact * or qualifier.* items. Ordinary entities and
// information registers use their physical metadata layout. Virtual sources,
// other registers and unknown qualifiers stay incomplete. SQL emission is unaffected.
func (r *projectionKeyResolver) starKeys(scopeID int, item []tok) projectionKeySet {
	unknown := projectionKeySet{complete: false}
	qualifier := ""
	if len(item) == 3 && item[0].kind == tIdent && item[1].kind == tDot && item[2].kind == tStar {
		qualifier = lowerFast(item[0].val)
	} else if len(item) != 1 || item[0].kind != tStar {
		return unknown
	}
	type source struct {
		qualifier string
		keys      projectionKeySet
	}
	var sources []source
	scope := r.ctx.scopes[scopeID]
	for alias, child := range scope.derivedAliases {
		sources = append(sources, source{alias, r.scopeKeys(child)})
	}
	for i := 0; i+2 < len(r.tokens); i++ {
		t := r.tokens[i]
		id, ok := r.ctx.scopeIDAt(i)
		if !ok || id != scopeID || r.ctx.sectionAt(i) != sectionFrom || t.kind != tIdent || !isSourceType(upperFast(t.val)) || r.tokens[i+1].kind != tDot || r.tokens[i+2].kind != tIdent {
			continue
		}
		alias := sourceToTable(upperFast(t.val), r.tokens[i+2].val)
		if i+4 < len(r.tokens) && r.tokens[i+3].kind == tIdent && r.tokens[i+4].kind == tIdent {
			if kw, ok := sqlKW(r.tokens[i+3].val); ok && kw == "AS" {
				alias = lowerFast(r.tokens[i+4].val)
			}
		}
		keys := unknown
		if i+3 >= len(r.tokens) || r.tokens[i+3].kind != tDot {
			if isInfoRegType(upperFast(t.val)) {
				for _, reg := range r.opts.InfoRegs {
					if strings.EqualFold(reg.Name, r.tokens[i+2].val) {
						keys = infoRegisterProjectionKeys(reg)
						break
					}
				}
			} else if !isAccumRegType(upperFast(t.val)) && !isAccountRegType(upperFast(t.val)) {
				for _, ent := range r.opts.Entities {
					if strings.EqualFold(ent.Name, r.tokens[i+2].val) {
						keys = entityProjectionKeys(ent)
						break
					}
				}
			}
		}
		sources = append(sources, source{alias, keys})
	}
	out := projectionKeySet{counts: map[string]int{}, complete: true}
	if qualifier != "" {
		matched := 0
		for _, s := range sources {
			if s.qualifier == qualifier {
				out.add(s.keys)
				matched++
			}
		}
		if matched != 1 {
			return unknown
		}
		return out
	}
	// Bare * includes every source, including auto-JOINs emitted for reference
	// navigation. Until those extra rows' column sets are proven, fail closed.
	if len(sources) == 0 || len(sources) != scope.sourceCount || len(preScanRefDims(r.tokens, r.opts)) > 0 {
		return unknown
	}
	for _, s := range sources {
		out.add(s.keys)
	}
	return out
}

// Match the physical entity layout: references expose their _id column, not
// the logical DSL name. System columns are reserved even when not user fields.
func entityProjectionKeys(ent *metadata.Entity) projectionKeySet {
	out := projectionKeySet{counts: map[string]int{"id": 1, "deletion_mark": 1, "_version": 1}, complete: true}
	if ent.Kind == metadata.KindDocument {
		out.counts["posted"] = 1
	}
	if ent.Hierarchical {
		out.counts["parent_id"] = 1
		out.counts["is_folder"] = 1
	}
	if ent.Kind == metadata.KindCatalog && len(ent.Predefined) > 0 {
		out.counts["_predefined_name"] = 1
		out.counts["_is_predefined"] = 1
	}
	for _, field := range ent.Fields {
		out.counts[metadata.ColumnName(field)] = 1
	}
	return out
}

// Match CreateInfoRegisterSQL, including reference _id columns and the
// service columns present in both periodic and nonperiodic registers. Entity
// system columns do not belong to an information register.
func infoRegisterProjectionKeys(reg *metadata.InfoRegister) projectionKeySet {
	out := projectionKeySet{counts: map[string]int{"recorder": 1, "recorder_type": 1, "updated_at": 1}, complete: true}
	if reg.Periodic {
		out.counts["period"] = 1
	}
	for _, field := range reg.Dimensions {
		out.counts[metadata.ColumnName(field)]++
	}
	for _, field := range reg.Resources {
		out.counts[metadata.ColumnName(field)]++
	}
	return out
}

// scopeKeys recursively follows derived boundaries, reserving unknown-type
// expressions with explicit aliases too. No type is inferred by this resolver.
func (r *projectionKeyResolver) scopeKeys(scopeID int) projectionKeySet {
	if out, ok := r.outputs[scopeID]; ok {
		return out
	}
	r.outputs[scopeID] = projectionKeySet{complete: false} // recursion fence
	out := projectionKeySet{counts: map[string]int{}, complete: true}
	start := -1
	for i, t := range r.tokens {
		if id, ok := r.ctx.scopeIDAt(i); ok && id == scopeID && t.kind == tIdent {
			if kw, ok := sqlKW(t.val); ok && kw == "SELECT" {
				start = i
				break
			}
		}
	}
	if start < 0 {
		return projectionKeySet{complete: false}
	}
	end := start + 1
	for end < len(r.tokens) && r.ctx.tokenSection[end] == sectionSelect && r.ctx.tokenScope[end] == scopeID {
		end++
	}
	for i := end; i < len(r.tokens) && r.ctx.tokenDepth[i] >= r.ctx.tokenDepth[start]; i++ {
		if r.ctx.tokenDepth[i] == r.ctx.tokenDepth[start] && r.tokens[i].kind == tIdent {
			if kw, ok := sqlKW(r.tokens[i].val); ok && kw == "UNION" {
				out.complete = false
			}
		}
	}
	from := selectProjectionStart(r.tokens, start)
	for _, item := range splitProjectionItems(r.tokens[from:end]) {
		col, _ := parseProjectionItem(item)
		if col.Star {
			out.add(r.starKeys(scopeID, item))
		} else {
			key, known := r.outputSQLKey(col, from, from+len(item))
			out.complete = out.complete && known && key != ""
			if key != "" {
				out.counts[metadata.SQLIdent(key)]++
			}
		}
		from += len(item) + 1
	}
	r.outputs[scopeID] = out
	return out
}
