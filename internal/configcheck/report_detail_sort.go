package configcheck

import (
	"strings"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/report"
)

// reportDetailSortColumns names known, unique outputs of an ordinary report.
// It does not turn an arbitrary spelling in sort into a declared result field.
func reportDetailSortColumns(proj *project.Project, rep *report.Report) map[string]bool {
	if rep.Query == "" {
		return nil
	}
	params := make(map[string]any, len(rep.Params))
	for _, p := range rep.Params {
		params[p.Name] = nil
	}
	compiled, err := query.Compile(rep.Query, query.CompileOpts{Params: params, Entities: proj.Entities, Registers: proj.Registers, InfoRegs: proj.InfoRegisters, AccountRegs: proj.AccountRegisters})
	if err != nil || !compiled.Projection.Simple {
		return nil
	}
	counts := map[string]int{}
	for _, col := range compiled.Projection.Columns {
		if col.Star || col.Output == "" {
			return nil
		}
		key := strings.ToLower(col.Output)
		if col.Alias == "" {
			if actual := compiled.DSLColumnAliases[key]; actual != "" {
				key = actual
			}
			if len(col.Path) > 0 && (strings.EqualFold(col.Path[len(col.Path)-1], "Ссылка") || strings.EqualFold(col.Path[len(col.Path)-1], "Ref")) {
				// Implicit reference names are rewritten by the compiler. If more than
				// one such key exists, require an explicit alias rather than guess it.
				if len(compiled.RefColumns) != 1 {
					continue
				}
				for actual := range compiled.RefColumns {
					key = actual
				}
			}
		}
		if full := compiled.LongIdents[key]; full != "" {
			key = full
		}
		counts[strings.ToLower(key)]++
	}
	fields := map[string]bool{}
	for name, count := range counts {
		if count == 1 {
			fields[name] = true
		}
	}
	return fields
}

func detailOnlySortColumns(c *report.Composition, outputs map[string]bool) map[string]bool {
	if !c.Detail || len(c.Columns) > 0 {
		return nil
	}
	fields := map[string]bool{}
	for name := range outputs {
		fields[name] = true
	}
	// Keep existing grouping/measure validation semantics: this extension is
	// only for source columns used to order the leaf records.
	for _, name := range c.Groupings {
		delete(fields, strings.ToLower(name))
	}
	for _, m := range c.Measures {
		delete(fields, strings.ToLower(m.Field))
	}
	return fields
}
