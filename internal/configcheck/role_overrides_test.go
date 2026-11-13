package configcheck

import (
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"testing"
)

func TestRoleOverrideUnsafeBackgroundRejected(t *testing.T) {
	bad, good, empty := "red;background-image:url(https://invalid/)", "#f3f6fa", ""
	entries := []metadata.RoleOverride{{Background: &bad}, {Background: &good}, {Background: &empty}}
	p := &project.Project{Widgets: []*metadata.Widget{{Name: "Probe", RoleOverrides: entries}}, HomePage: &metadata.HomePage{SidePanel: &metadata.HomeSidePanel{RoleOverrides: entries}}}
	issues := CheckWidgetAppearance(p)
	if len(issues) != 2 {
		t.Fatalf("expected widget and panel diagnostics, got %#v", issues)
	}
}
