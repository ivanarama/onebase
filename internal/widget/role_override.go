package widget

import (
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"strings"
)

func overrideMatches(entry metadata.RoleOverride, user *auth.User) bool {
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
	for _, name := range entry.ExceptRoles {
		if has(name) {
			return false
		}
	}
	if entry.IncludeAdmin && user != nil && user.IsAdmin {
		return true
	}
	if len(entry.Roles) == 0 {
		return true
	}
	for _, name := range entry.Roles {
		if has(name) {
			return true
		}
	}
	return false
}

// OverridesVisible never grants access denied by the widget's own role selector.
func OverridesVisible(entries []metadata.RoleOverride, user *auth.User) bool {
	visible := true
	for _, entry := range entries {
		if entry.Visible != nil && overrideMatches(entry, user) {
			visible = *entry.Visible
		}
	}
	return visible
}

// OverrideBackground returns a per-request value without mutating shared metadata.
// The caller still validates the resulting value through csssafe.Color.
func OverrideBackground(base string, entries []metadata.RoleOverride, user *auth.User) string {
	for _, entry := range entries {
		if entry.Background != nil && overrideMatches(entry, user) {
			base = *entry.Background
		}
	}
	return base
}
