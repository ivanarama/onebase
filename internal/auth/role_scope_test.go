package auth

import "testing"

func TestRoleScopeExclusionsWinOverAdminAndMixedRoles(t *testing.T) {
	for _, tc := range []struct {
		name  string
		admin bool
		roles []string
		want  bool
	}{
		{"otk", false, []string{"OTK"}, true}, {"admin", true, nil, true},
		{"operator", false, []string{"Operator"}, false}, {"messenger", false, []string{"Messenger"}, false},
		{"mixed", false, []string{"OTK", "Messenger"}, false}, {"admin-operator", true, []string{"Operator"}, false},
		{"unknown", false, []string{"Other"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := &User{IsAdmin: tc.admin}
			for _, name := range tc.roles {
				u.Roles = append(u.Roles, &Role{Name: name})
			}
			if got := RoleScopeMatches(u, []string{"OTK"}, []string{"Operator", "Messenger"}, true); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
	if !RoleScopeMatches(nil, nil, nil, true) || !RoleScopeMatches(nil, nil, nil, false) {
		t.Fatal("empty allowlist must match all non-excluded users")
	}
}
