package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/navigation"
	"golang.org/x/net/html"
)

func loadRoleHome(t *testing.T, f navigationHTTPFixture, yaml string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "home_page.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	home, err := metadata.LoadHomePage(path)
	if err != nil {
		t.Fatal(err)
	}
	f.server.reg.LoadHomePage(home)
}

func roleHomeNav(t *testing.T, f navigationHTTPFixture, login string) *html.Node {
	t.Helper()
	response := f.request(t, "GET", "/ui/", login, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("home: %d %s", response.Code, response.Body.String())
	}
	doc, err := html.Parse(strings.NewReader(response.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	nav := semanticFind(doc, "id", "ob-nav")
	if nav == nil {
		t.Fatal("home navigation missing")
	}
	return nav
}

func TestHomeNavByRole_HTTPKeepsSemanticMenuAndSettings(t *testing.T) {
	f := newNavigationHTTPFixture(t)
	loadRoleHome(t, f, `nav:
  catalogs: [Years, Classes]
nav_by_role:
  Teacher:
    catalogs: [Classes]
menu:
  sections:
    - id: school
      title: School
      icon: calendar-days
      groups:
        - id: school-work
          title: School work
          items:
            - id: years
              target: catalog:Years
            - id: classes
              title: Teaching classes
              target: catalog:Classes
`)
	for _, login := range []string{"alice", "admin"} {
		nav := roleHomeNav(t, f, login)
		for _, id := range []string{"cfg:school", "cfg:school-work", "cfg:classes"} {
			if semanticFind(nav, "data-nav-id", id) == nil {
				t.Fatalf("%s lost %s", login, id)
			}
		}
		if !strings.Contains(semanticHeading(nav), "Teaching classes") {
			t.Fatal("configured item title lost")
		}
		if (semanticFind(nav, "data-nav-id", "cfg:years") != nil) != (login == "admin") {
			t.Fatalf("%s role membership", login)
		}
	}
	// The common layer keeps the global layout; its stable IDs also work in
	// the role projection and in the personal editing surface.
	common := f.editor(t, "")
	common.Desired.Sections[0].Groups[0].Title = "Common work"
	if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, common)); rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	state := f.personalEditor(t, "alice", "")
	group := &state.Desired.Sections[0].Groups[0]
	if group.ID != "cfg:school-work" || group.Title != "Common work" || len(group.Items) != 1 || group.Items[0].ID != "cfg:classes" {
		t.Fatalf("personal menu: %+v", state)
	}
	group.Title = "My teaching"
	preview := f.request(t, "POST", "/ui/settings/navigation/preview", "alice", personalNavigationForm(t, state))
	if preview.Code != 200 || !strings.Contains(preview.Body.String(), "My teaching") {
		t.Fatal(preview.Code, preview.Body.String())
	}
	personalSave(t, f, "alice", state)
	if nav := roleHomeNav(t, f, "alice"); !strings.Contains(semanticHeading(nav), "My teaching") || semanticFind(nav, "data-nav-id", "cfg:classes") == nil {
		t.Fatal("personal menu/runtime disagree")
	}
	if nav := roleHomeNav(t, f, "admin"); strings.Contains(semanticHeading(nav), "My teaching") || semanticFind(nav, "data-nav-id", "cfg:years") == nil {
		t.Fatal("role/personal state changed admin menu")
	}
}

func TestHomeNavByRole_HTTPPersonalScopeAndRevision(t *testing.T) {
	f := newNavigationHTTPFixture(t)
	yaml := `nav:
  catalogs: [Years]
nav_by_role:
  Teacher:
    catalogs: [Classes]
`
	loadRoleHome(t, f, yaml)
	state := f.personalEditor(t, "alice", "")
	for _, tree := range []navigation.Tree{state.Base, state.Desired} {
		if len(tree.Sections) != 1 || len(tree.Sections[0].Items) != 1 || tree.Sections[0].Items[0].Target != "catalog:Classes" {
			t.Fatalf("role palette: %+v", tree)
		}
	}
	nav := roleHomeNav(t, f, "alice")
	links := strings.Join(semanticLinks(nav), "|")
	if !strings.Contains(links, "/ui/catalog/Classes") || strings.Contains(links, "/ui/catalog/Years") {
		t.Fatal(links)
	}
	forged := state
	forged.Desired = f.editor(t, "").Desired // Years is readable, but outside this role's menu.
	for _, action := range []string{"preview", "save"} {
		rec := f.request(t, "POST", "/ui/settings/navigation/"+action, "alice", personalNavigationForm(t, forged))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("out-of-scope %s: %d %s", action, rec.Code, rec.Body.String())
		}
	}
	state.Desired.Sections[0].Title = "My role menu"
	state.Renamed = []string{state.Desired.Sections[0].ID}
	preview := f.request(t, "POST", "/ui/settings/navigation/preview", "alice", personalNavigationForm(t, state))
	var result struct {
		Preview []NavigationPreviewSection `json:"preview"`
	}
	if preview.Code != 200 {
		t.Fatal(preview.Code, preview.Body.String())
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Preview) != 1 || result.Preview[0].Title != "My role menu" || len(result.Preview[0].Items) != 1 || result.Preview[0].Items[0].URL != "/ui/catalog/Classes" {
		t.Fatalf("preview: %+v", result)
	}
	personalSave(t, f, "alice", state)
	saved := f.personalEditor(t, "alice", "")
	if saved.Desired.Sections[0].Title != "My role menu" || !strings.Contains(semanticHeading(roleHomeNav(t, f, "alice")), "My role menu") {
		t.Fatal("save/runtime disagree")
	}
	// Changing role membership invalidates both preview and save, even though
	// the common nav and the stored personal revision did not change.
	loadRoleHome(t, f, strings.Replace(yaml, "catalogs: [Classes]", "catalogs: [Years]", 1))
	fresh := f.personalEditor(t, "alice", "")
	if fresh.BaseRevision == saved.BaseRevision {
		t.Fatal("role membership did not change base revision")
	}
	for _, action := range []string{"preview", "save"} {
		rec := f.request(t, "POST", "/ui/settings/navigation/"+action, "alice", personalNavigationForm(t, saved))
		if rec.Code != http.StatusConflict {
			t.Fatalf("stale %s: %d %s", action, rec.Code, rec.Body.String())
		}
	}
	// An explicitly empty role must not become the global legacy flat scope.
	loadRoleHome(t, f, "nav:\n  catalogs: [Years]\nnav_by_role:\n  Teacher: {}\n")
	empty := f.personalEditor(t, "alice", "")
	if len(empty.Base.Sections) != 0 || len(empty.Desired.Sections) != 0 || len(empty.Preview) != 0 {
		t.Fatalf("empty role menu: %+v", empty)
	}
	if links := strings.Join(semanticLinks(roleHomeNav(t, f, "alice")), "|"); strings.Contains(links, "/ui/catalog/") {
		t.Fatal(links)
	}
	admin := f.personalEditor(t, "admin", "")
	if len(admin.Base.Sections) != 1 || admin.Base.Sections[0].Items[0].Target != "catalog:Years" {
		t.Fatal("admin lost common nav")
	}
}

func TestHomeNavByRole_HTTPUnplacedRoleTargets(t *testing.T) {
	f := newNavigationHTTPFixture(t)
	loadRoleHome(t, f, `nav:
  catalogs: [Years, Classes]
nav_by_role:
  Teacher:
    catalogs: [Years, Classes]
    documents: [Orders]
menu:
  sections:
    - id: school
      title: School
      items:
        - id: years
          target: catalog:Years
`)
	state := f.personalEditor(t, "alice", "")
	if len(state.Base.Sections) != 2 || state.Base.Sections[0].ID != "cfg:school" || state.Base.Sections[0].Items[0].ID != "cfg:years" || state.Base.Sections[1].ID != "cfg:other" {
		t.Fatalf("configured role tree: %+v", state.Base)
	}
	other := state.Base.Sections[1].Items
	if len(other) != 2 || other[0].Target != "catalog:Classes" || other[1].Target != "document:Orders" {
		t.Fatalf("unplaced role targets duplicated or missing: %+v", other)
	}
	links := strings.Join(semanticLinks(roleHomeNav(t, f, "alice")), "|")
	for _, target := range []string{"/ui/catalog/Years", "/ui/catalog/Classes", "/ui/document/Orders"} {
		if !strings.Contains(links, target) {
			t.Fatalf("missing role link %s: %s", target, links)
		}
	}
	admin := f.editor(t, "")
	if len(admin.Base.Sections) != 2 || len(admin.Base.Sections[1].Items) != 1 || admin.Base.Sections[1].Items[0].Target != "catalog:Classes" {
		t.Fatal("role projection mutated the common configuration")
	}
}

func TestHomeNavByRole_HTTPCommonMoveBeforeRoleProjection(t *testing.T) {
	for _, grouped := range []bool{false, true} {
		t.Run(fmt.Sprintf("grouped=%v", grouped), func(t *testing.T) {
			f := newNavigationHTTPFixture(t)
			layout := `nav:
  catalogs: [Years, Classes]
nav_by_role:
  Teacher:
    catalogs: [Classes]
menu:
  sections:
    - id: years-section
      title: Years section
      items:
        - id: years
          target: catalog:Years
    - id: classes-section
      title: Classes section
      items:
        - id: classes
          target: catalog:Classes
`
			if grouped {
				layout = strings.Replace(layout, "      items:\n        - id: years\n          target: catalog:Years", "      groups:\n        - id: years-group\n          title: Years group\n          items:\n            - id: years\n              target: catalog:Years", 1)
			}
			loadRoleHome(t, f, layout)
			stale := f.personalEditor(t, "alice", "")
			common := f.editor(t, "")
			item := common.Desired.Sections[1].Items[0]
			common.Desired.Sections[1].Items = nil
			if grouped {
				common.Desired.Sections[0].Groups[0].Items = append(common.Desired.Sections[0].Groups[0].Items, item)
			} else {
				common.Desired.Sections[0].Items = append(common.Desired.Sections[0].Items, item)
			}
			if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, common)); rec.Code != http.StatusSeeOther {
				t.Fatal(rec.Code, rec.Body.String())
			}
			state := f.personalEditor(t, "alice", "")
			if stale.BaseRevision == state.BaseRevision {
				t.Fatal("common move did not invalidate role revision")
			}
			for _, tree := range []navigation.Tree{state.Base, state.Desired} {
				if len(tree.Sections) != 1 || tree.Sections[0].ID != "cfg:years-section" {
					t.Fatalf("common destination lost: %+v", tree)
				}
				items := tree.Sections[0].Items
				if grouped {
					if len(tree.Sections[0].Groups) != 1 || tree.Sections[0].Groups[0].ID != "cfg:years-group" {
						t.Fatalf("common destination group lost: %+v", tree)
					}
					items = tree.Sections[0].Groups[0].Items
				}
				if len(items) != 1 || items[0].ID != "cfg:classes" {
					t.Fatalf("role move or membership lost: %+v", tree)
				}
			}
			nav := roleHomeNav(t, f, "alice")
			if semanticFind(nav, "data-nav-id", "cfg:years-section") == nil || semanticFind(nav, "data-nav-id", "cfg:classes") == nil || semanticFind(nav, "data-nav-id", "cfg:years") != nil {
				t.Fatal("runtime does not apply common move within role scope")
			}
			for _, action := range []string{"preview", "save"} {
				if rec := f.request(t, "POST", "/ui/settings/navigation/"+action, "alice", personalNavigationForm(t, stale)); rec.Code != http.StatusConflict {
					t.Fatalf("stale %s: %d", action, rec.Code)
				}
			}
			state.Desired.Sections[0].Title = "My moved classes"
			state.Desired.Sections[0].Titles = nil
			state.Renamed = []string{"cfg:years-section"}
			preview := f.request(t, "POST", "/ui/settings/navigation/preview", "alice", personalNavigationForm(t, state))
			if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), "My moved classes") {
				t.Fatal(preview.Code, preview.Body.String())
			}
			personalSave(t, f, "alice", state)
			if !strings.Contains(semanticHeading(roleHomeNav(t, f, "alice")), "My moved classes") {
				t.Fatal("personal save lost common placement")
			}
			// The admin still sees its complete menu, including the item used
			// as the common move's predecessor but removed by the role filter.
			if semanticFind(roleHomeNav(t, f, "admin"), "data-nav-id", "cfg:years") == nil {
				t.Fatal("role projection changed common tree")
			}
			common = f.editor(t, "")
			if grouped {
				common.Desired.Sections[0].Groups[0].Items = common.Desired.Sections[0].Groups[0].Items[:1]
			} else {
				common.Desired.Sections[0].Items = common.Desired.Sections[0].Items[:1]
			}
			if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, common)); rec.Code != http.StatusSeeOther {
				t.Fatal(rec.Code, rec.Body.String())
			}
			hidden := f.personalEditor(t, "alice", "")
			if len(hidden.Desired.Sections) != 0 || semanticFind(roleHomeNav(t, f, "alice"), "data-nav-id", "cfg:classes") != nil {
				t.Fatal("role projection resurrected a common hidden target")
			}
		})
	}
}
