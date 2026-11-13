package widget

import (
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"testing"
)

func TestRoleOverrideCannotBroadenBaseRoles(t *testing.T) {
	yes, no := true, false
	red, empty := "#f00", ""
	admin := &auth.User{IsAdmin: true}
	w := &metadata.Widget{Roles: []string{"OTK"}, RoleOverrides: []metadata.RoleOverride{{IncludeAdmin: true, Visible: &yes}}}
	if VisibleFor(w, admin) {
		t.Fatal("override granted a base-hidden widget")
	}
	entries := []metadata.RoleOverride{{Visible: &no, Background: &red}, {Visible: &yes, Background: &empty}}
	if !OverridesVisible(entries, admin) || OverrideBackground("#fff", entries, admin) != "" {
		t.Fatal("ordered overrides/explicit empty lost")
	}
}
