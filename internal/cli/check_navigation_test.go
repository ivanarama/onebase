package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configcheck"
)

func semanticCheckFixture(t *testing.T, menu string) string {
	t.Helper()
	dir := checkFixture(t, false)
	writeProcrunFixture(t, dir, "documents/Order.yaml", "name: Order\nfields:\n  - name: Amount\n    type: number\n")
	writeProcrunFixture(t, dir, "subsystems/Education.yaml", "name: Education\ncontents:\n  catalogs: [Товар]\n  documents: [Order]\n  reports: [ПоТовару]\n"+menu)
	return dir
}

const checkSchoolMenu = `menu:
  sections:
    - id: academic-years
      title: Учебные годы
      titles:
        en: Academic years
      items:
        - id: classes
          target: catalog:Товар
      groups:
        - id: orders
          title: Приказы
          items:
            - id: order
              target: document:Order
            - id: report
              target: report:ПоТовару
`

func TestCheckSemanticNavigation(t *testing.T) {
	for _, tc := range []struct{ name, menu, diagnostic string }{
		{"mixed school", checkSchoolMenu, ""},
		{"unknown target", strings.Replace(checkSchoolMenu, "catalog:Товар", "catalog:Unknown", 1), "navigation.membership"},
		{"wrong kind", strings.Replace(checkSchoolMenu, "catalog:Товар", "document:Товар", 1), "navigation.membership"},
		{"URL", strings.Replace(checkSchoolMenu, "catalog:Товар", "https://example.com", 1), "navigation.target"},
		{"register view", strings.Replace(checkSchoolMenu, "catalog:Товар", "register:X:totals", 1), "navigation.target"},
		{"duplicate id", strings.Replace(checkSchoolMenu, "id: order\n", "id: classes\n", 1), "navigation.duplicate-id"},
		{"reserved id", strings.Replace(checkSchoolMenu, "id: classes", "id: other", 1), "navigation.reserved-id"},
		{"non ascii id", strings.Replace(checkSchoolMenu, "id: classes", "id: классы", 1), "navigation.id"},
		{"unknown kind", strings.Replace(checkSchoolMenu, "catalog:Товар", "folder:Товар", 1), "navigation.target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runCheckCmd(t, runCheck, semanticCheckFixture(t, tc.menu), map[string]string{"json": "true", "lint": "true"})
			var result configcheck.Result
			if parseErr := json.Unmarshal([]byte(out), &result); parseErr != nil {
				t.Fatalf("%v: %s (command: %v)", parseErr, out, err)
			}
			if tc.diagnostic == "" {
				if err != nil || !result.OK {
					t.Fatalf("valid menu rejected: %v %s", err, out)
				}
				return
			}
			if err == nil || result.OK {
				t.Fatalf("invalid menu accepted: %s", out)
			}
			for _, issue := range result.Issues {
				if issue.Code == tc.diagnostic && strings.Contains(issue.File, "Education") {
					return
				}
			}
			t.Fatalf("missing %s: %s", tc.diagnostic, out)
		})
	}
}

func TestCheckSemanticMenuOutsideContents(t *testing.T) {
	dir := semanticCheckFixture(t, checkSchoolMenu)
	writeProcrunFixture(t, dir, "catalogs/Outside.yaml", "name: Outside\nfields:\n  - name: Title\n    type: string\n")
	writeProcrunFixture(t, dir, "subsystems/Education.yaml", "name: Education\ncontents:\n  catalogs: [Товар]\n"+strings.Replace(checkSchoolMenu, "catalog:Товар", "catalog:Outside", 1))
	out, err := runCheckCmd(t, runCheck, dir, map[string]string{"json": "true"})
	if err == nil || !strings.Contains(out, "navigation.membership") {
		t.Fatalf("outside member accepted: %v %s", err, out)
	}
}

func TestCheckRejectsMenuDepthAndUnknownFields(t *testing.T) {
	for _, extra := range []string{"url: https://example.com", "groups: []"} {
		t.Run(extra, func(t *testing.T) {
			menu := checkSchoolMenu + "              " + extra + "\n"
			out, err := runCheckCmd(t, runCheck, semanticCheckFixture(t, menu), map[string]string{"json": "true"})
			if err == nil {
				t.Fatalf("unsupported menu shape accepted: %s", out)
			}
			if !strings.Contains(out+fmt.Sprint(err), "field ") {
				t.Fatalf("missing unknown-field diagnostic: %v %s", err, out)
			}
		})
	}
}

func TestCheckMenuPlacement(t *testing.T) {
	dir := semanticCheckFixture(t, "home_page:\n  menu:\n    sections: []\n")
	out, err := runCheckCmd(t, runCheck, dir, map[string]string{"json": "true"})
	if err == nil || !strings.Contains(out, "navigation.placement") {
		t.Fatalf("nested menu silently ignored: %v %s", err, out)
	}
}
