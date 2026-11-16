package access

import (
	"context"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
)

type reportOutputContextKey struct{}

// WithReportOutput marks a server-selected report execution. No request header,
// browser context or global process flag can enable/disable this policy.
func WithReportOutput(ctx context.Context) context.Context {
	return context.WithValue(ctx, reportOutputContextKey{}, true)
}

func IsReportOutput(ctx context.Context) bool {
	v, _ := ctx.Value(reportOutputContextKey{}).(bool)
	return v
}

func ReportOutputFieldDecisions(u *auth.User, kind, name string, meta *metadata.Entity) map[string]FieldDecision {
	decisions := FieldDecisions(u, kind, name, meta)
	if decisions == nil {
		decisions = map[string]FieldDecision{}
	}
	for field, decision := range ReportOutputDecisions(meta) {
		decisions[field] = decision
	}
	return decisions
}

// ReportOutputDecisions is mandatory output policy, independent of roles and
// admin/disclosure privileges. Full addresses are hidden rather than parsed
// heuristically. Labels of a protected entity are untrusted derived data.
func ReportOutputDecisions(meta *metadata.Entity) map[string]FieldDecision {
	if meta == nil {
		return nil
	}
	out := map[string]FieldDecision{}
	for _, field := range meta.Fields {
		if field.PII || field.ReportPII {
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
	for _, source := range res.SourceOccurrences {
		if !source.Virtual || source.TablePart != "" {
			continue
		}
		// VT membership can use hidden partition/order fields that never occur
		// in the written projection. Until an authorized operation graph proves
		// those dependencies, unused protected metadata is not proof of safety.
		meta := sourceMeta(source.Kind, source.Name, "", lookup)
		if meta == nil || len(ReportOutputDecisions(meta)) != 0 {
			return QueryMaskPlan{Denied: source.Name}
		}
	}
	return queryMaskPlanFor(u, res, lookup, true)
}
