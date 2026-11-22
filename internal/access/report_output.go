package access

import (
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
)

// ReportOutputDecisions is mandatory output policy, independent of roles and
// admin/disclosure privileges. Full addresses are hidden rather than parsed
// heuristically. Labels of a protected entity are untrusted derived data.
func ReportOutputDecisions(meta *metadata.Entity) map[string]FieldDecision {
	if meta == nil {
		return nil
	}
	out := map[string]FieldDecision{}
	for _, field := range meta.Fields {
		if field.PII {
			out[field.Name] = FieldDecision{Strategy: FieldHide}
		}
	}
	if len(out) == 0 {
		return nil
	}
	for _, field := range metadata.LabelFields(meta) {
		out[field.Name] = FieldDecision{Strategy: FieldHide}
	}
	return out
}

// ReportOutputFilterAllowed checks an already proven SOURCE field. The caller
// must still prove output lineage/type and enforce ordinary object/row/field
// permissions. An alias, expression or saved client descriptor is not proof.
func ReportOutputFilterAllowed(meta *metadata.Entity, field string) bool {
	if meta == nil {
		return false
	}
	for name := range ReportOutputDecisions(meta) {
		if strings.EqualFold(name, field) {
			return false
		}
	}
	for _, f := range meta.Fields {
		if strings.EqualFold(f.Name, field) {
			return true
		}
	}
	return strings.EqualFold(field, "id") || strings.EqualFold(field, "Ссылка")
}

// ReportOutputQueryMaskPlan adds mandatory output restrictions to ordinary
// role masking. Use only at report boundaries; cards and ordinary queries keep
// their existing permissions. Filters/grouping on protected data fail closed.
func ReportOutputQueryMaskPlan(u *auth.User, res query.Result, lookup func(kind, name string) *metadata.Entity) QueryMaskPlan {
	return queryMaskPlanFor(u, res, lookup, true)
}
