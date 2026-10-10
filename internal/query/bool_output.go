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
	types := scalarProjectionTypes(tokens, ctx, scopeID, scoped[scopeID], qualified[scopeID])
	var candidates []string
	counts := map[string]int{}
	for _, item := range splitProjectionItems(tokens[start+1 : topLevelFrom(tokens, start)]) {
		col, _ := parseProjectionItem(item)
		if col.Output == "" {
			continue
		}
		output := col.Output
		if col.Alias == "" && ctx.scopeProjectsSystemColumn(tokens, scopeID, output, map[int]bool{}) {
			if physical, _, system := entitySystemColAlias(output); system {
				output = physical
			}
		}
		counts[output]++
		if len(col.Path) > 0 && types[col.Output] == metadata.FieldTypeBool {
			candidates = append(candidates, output)
		}
	}
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
