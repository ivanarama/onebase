package access_test

import (
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"testing"
)

func TestFieldDiscloseDenied(t *testing.T) {
	no, yes := false, true
	deny := catRole([]string{"read", "disclose"}, auth.FieldPolicies{"Адрес": {Read: "hide", Disclose: &no}})
	allow := catRole([]string{"read"}, auth.FieldPolicies{"Адрес": {Read: "hide", Disclose: &yes}})
	unspecified := catRole([]string{"read"}, nil)
	nonReading := catRole([]string{"disclose"}, nil)
	for _, tc := range []struct {
		name  string
		roles []*auth.Role
		admin bool
		want  bool
	}{
		{"denied", []*auth.Role{deny}, false, true},
		{"both-denied", []*auth.Role{deny, deny}, false, true},
		{"explicit-allow", []*auth.Role{deny, allow}, false, false},
		{"legacy-reading-role", []*auth.Role{deny, unspecified}, false, false},
		{"non-reading-role-does-not-vote", []*auth.Role{deny, nonReading}, false, true},
		{"unmasked-admin", []*auth.Role{deny}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := &auth.User{Roles: tc.roles, IsAdmin: tc.admin}
			if got := access.FieldDiscloseDenied(u, "catalog", "Клиент", "аДрЕс"); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
