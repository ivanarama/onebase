package auth

import "strings"

// RoleScopeMatches selects presentation only. Exclusions always win, including
// for an administrator carrying an excluded role. An empty allowlist matches
// every non-excluded role; includeAdmin adds admins to a nonempty allowlist.
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
		return true
	}
	for _, name := range roles {
		if has(name) {
			return true
		}
	}
	return false
}
