package auth

import "strings"

// RoleScopeMatches selects presentation only. Exclusions always win, including
// for an administrator carrying an excluded role. includeAdmin without roles
// selects administrators only; a scope without selectors matches everyone.
func RoleScopeMatches(user *User, roles, exceptRoles []string, includeAdmin bool) bool {
	has := func(want string) bool {
		if user == nil {
			return false
		}
		for _, role := range user.Roles {
			if role != nil && strings.EqualFold(strings.TrimSpace(role.Name), strings.TrimSpace(want)) {
				return true
			}
		}
		return false
	}
	for _, name := range exceptRoles {
		if has(name) {
			return false
		}
	}
	if includeAdmin && user != nil && user.IsAdmin {
		return true
	}
	if len(roles) == 0 {
		return !includeAdmin
	}
	for _, name := range roles {
		if has(name) {
			return true
		}
	}
	return false
}
