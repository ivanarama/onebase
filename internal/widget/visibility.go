package widget

import (
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"strings"
)

// Role visibility narrows display; source permissions and row access still apply.
func VisibleFor(w *metadata.Widget, user *auth.User) bool {
	if w == nil || !OverridesVisible(w.RoleOverrides, user) {
		return false
	}
	has := func(name string) bool {
		if user != nil {
			for _, role := range user.Roles {
				if role != nil && strings.EqualFold(strings.TrimSpace(role.Name), strings.TrimSpace(name)) {
					return true
				}
			}
		}
		return false
	}
	for _, role := range w.ExceptRoles {
		if has(role) {
			return false
		}
	}
	if len(w.Roles) == 0 {
		return true
	}
	for _, role := range w.Roles {
		if has(role) {
			return true
		}
	}
	return false
}
