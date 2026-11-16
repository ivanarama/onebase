package resultfilter

import (
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"strings"
)

// ReportAllowed combines the mandatory report gate with ordinary object and
// field permissions. A reference UUID never overrides pii on its owner field.
func ReportAllowed(user *auth.User) Allowed {
	return func(src query.SourceRef, meta *metadata.Entity, field string, isUUID bool) bool {
		if user != nil && !user.Has(src.Kind, src.Name, "read") {
			return false
		}
		if !access.ReportOutputFilterAllowed(meta, field) {
			return false
		}
		self := isUUID && (strings.EqualFold(field, "id") || strings.EqualFold(field, "Ссылка"))
		for name, decision := range access.FieldDecisions(user, src.Kind, src.Name, meta) {
			same := strings.EqualFold(name, field) || (self && (strings.EqualFold(name, "id") || strings.EqualFold(name, "Ссылка")))
			if same && decision.Masked() {
				return false
			}
		}
		return true
	}
}
