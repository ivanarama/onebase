package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/navigation"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

// The two roles retain identical read permissions. Only their navigation
// composition differs, so this exercises role projection rather than RBAC.
func roleIntentFixture(t *testing.T, db *storage.DB, roleOnly bool) (navigationHTTPFixture, func(bool)) {
	t.Helper()
	dir := t.TempDir()
	layout := `sections:
  - id: classes-section
    title: Classes section
    groups:
      - id: classes-group
        title: Classes group
        items:
          - id: classes
            target: catalog:Classes
  - id: years-section
    title: Years section
    items:
      - id: years
        target: catalog:Years
`
	roles := `nav_by_role:
  Teacher:
    catalogs: [Classes, Years]
  LimitedTeacher:
    catalogs: [Years]
`
	home := "nav:\n  catalogs: [Classes, Years]\n" + roles + "menu:\n" + indentRoleIntentLayout(layout)
	if roleOnly {
		home = "nav:\n  catalogs: [Years]\n" + roles
	}
	files := map[string]string{
		"config/app.yaml":        "name: RoleIntent\n",
		"config/home_page.yaml":  home,
		"catalogs/Classes.yaml":  "name: Classes\ntitle: Classes\nfields:\n  - name: Name\n    type: string\n",
		"catalogs/Years.yaml":    "name: Years\ntitle: Years\nfields:\n  - name: Name\n    type: string\n",
		"roles/Teacher.yaml":     "name: Teacher\npermissions:\n  catalogs:\n    Classes: [read]\n    Years: [read]\n",
		"subsystems/School.yaml": "name: School\ntitle: School\ncontents:\n  catalogs: [Classes, Years]\nhome_page:\n" + indentRoleIntentLayout(roles) + "menu:\n" + indentRoleIntentLayout(layout),
	}
	for name, text := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	f := schoolNavigationFixture(t, db, dir, true)
	ctx := context.Background()
	if err := db.SaveNavCollapsible(ctx, true); err != nil {
		t.Fatal(err)
	}
	teacher, err := auth.LoadRoleFile(filepath.Join(dir, "roles/Teacher.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	limited := *teacher
	limited.Name, limited.ID = "LimitedTeacher", ""
	if err := f.server.authRepo.SyncRoles(ctx, []*auth.Role{&limited}); err != nil {
		t.Fatal(err)
	}
	rolesList, err := f.server.authRepo.ListRoles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range rolesList {
		if role.Name == "Teacher" {
			teacher.ID = role.ID
		}
	}
	alice, err := f.server.authRepo.GetByLogin(ctx, "alice")
	if err != nil || alice == nil || teacher.ID == "" {
		t.Fatalf("roles: %v", err)
	}
	switchRole := func(restricted bool) {
		t.Helper()
		add, remove := teacher.ID, limited.ID
		if restricted {
			add, remove = remove, add
		}
		if err := f.server.authRepo.AssignRole(ctx, alice.ID, add); err != nil {
			t.Fatal(err)
		}
		if err := f.server.authRepo.UnassignRole(ctx, alice.ID, remove); err != nil {
			t.Fatal(err)
		}
	}
	return f, switchRole
}

func indentRoleIntentLayout(text string) string {
	return "  " + strings.ReplaceAll(strings.TrimSuffix(text, "\n"), "\n", "\n  ") + "\n"
}

func roleIntentItem(t *testing.T, tree *navigation.Tree, target string) *navigation.Item {
	t.Helper()
	for i := range tree.Sections {
		section := &tree.Sections[i]
		for j := range section.Items {
			if section.Items[j].Target == target {
				return &section.Items[j]
			}
		}
		for j := range section.Groups {
			for k := range section.Groups[j].Items {
				if section.Groups[j].Items[k].Target == target {
					return &section.Groups[j].Items[k]
				}
			}
		}
	}
	t.Fatalf("missing target %s", target)
	return nil
}

func roleIntentRemove(tree *navigation.Tree, id string) {
	remove := func(items []navigation.Item) []navigation.Item {
		result := []navigation.Item{}
		for _, item := range items {
			if item.ID != id {
				result = append(result, item)
			}
		}
		return result
	}
	for i := range tree.Sections {
		tree.Sections[i].Items = remove(tree.Sections[i].Items)
		for j := range tree.Sections[i].Groups {
			tree.Sections[i].Groups[j].Items = remove(tree.Sections[i].Groups[j].Items)
		}
	}
}

func roleIntentNav(t *testing.T, f navigationHTTPFixture, login, sub string) *html.Node {
	t.Helper()
	response := f.request(t, "GET", "/ui/?"+url.Values{"subsystem": {sub}}.Encode(), login, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("menu: %d %s", response.Code, response.Body.String())
	}
	doc, err := html.Parse(strings.NewReader(response.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	nav := semanticFind(doc, "id", "ob-nav")
	if nav == nil {
		t.Fatal("missing menu")
	}
	return nav
}

func TestPersonalNavigation_RoleScopeRetainsIntentHTTPMatrix(t *testing.T) {
	for _, scope := range []string{"home", "School", "home-role-only"} {
		for _, intent := range []string{"rename", "move", "hide"} {
			for _, save := range []string{"none", "unchanged", "edit-years"} {
				t.Run(scope+"/"+intent+"/"+save, func(t *testing.T) {
					dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
						f, switchRole := roleIntentFixture(t, db, scope == "home-role-only")
						sub := ""
						if scope == "School" {
							sub = scope
						}
						state := f.personalEditor(t, "alice", sub)
						item := *roleIntentItem(t, &state.Desired, "catalog:Classes")
						classID := item.ID
						yearsID := roleIntentItem(t, &state.Desired, "catalog:Years").ID
						switch intent {
						case "rename":
							target := roleIntentItem(t, &state.Desired, "catalog:Classes")
							target.Title, target.Titles = "Personal Classes", nil
							state.Renamed = []string{classID}
						case "move":
							roleIntentRemove(&state.Desired, classID)
							state.Desired.Sections = append(state.Desired.Sections, navigation.Section{ID: "new:placement", Title: "Personal placement", Items: []navigation.Item{item}})
						case "hide":
							roleIntentRemove(&state.Desired, classID)
						}
						personalSave(t, f, "alice", state)
						full := f.personalEditor(t, "alice", sub)
						before, err := json.Marshal(full.Desired)
						if err != nil {
							t.Fatal(err)
						}
						bobBefore := f.personalEditor(t, "bob", sub)
						bobTree, err := json.Marshal(bobBefore.Desired)
						if err != nil {
							t.Fatal(err)
						}
						switchRole(true)
						editor := f.request(t, "GET", "/ui/settings/navigation?"+url.Values{"subsystem": {sub}}.Encode(), "alice", nil)
						limited := decodeNavigationBootstrap(t, editor)
						if limited.BaseRevision == full.BaseRevision {
							t.Fatal("role change did not invalidate revision")
						}
						for _, forbidden := range []string{"catalog:Classes", "Personal Classes", classID} {
							if strings.Contains(editor.Body.String(), forbidden) {
								t.Fatalf("restricted editor leaked %q", forbidden)
							}
						}
						if nav := roleIntentNav(t, f, "alice", sub); strings.Contains(strings.Join(semanticLinks(nav), "|"), "/ui/catalog/Classes") || strings.Contains(semanticHeading(nav), "Personal Classes") {
							t.Fatal("restricted runtime exposed Classes")
						}
						// Read permission is deliberately unchanged.
						if response := f.request(t, "GET", "/ui/catalog/classes", "alice", nil); response.Code != http.StatusOK {
							t.Fatalf("read permission changed: %d", response.Code)
						}
						for _, action := range []string{"preview", "save"} {
							if response := f.request(t, "POST", "/ui/settings/navigation/"+action, "alice", personalNavigationForm(t, full)); response.Code != http.StatusConflict {
								t.Fatalf("stale %s: %d", action, response.Code)
							}
							forged := limited
							forged.Desired = navigation.Tree{}
							raw, _ := json.Marshal(limited.Desired)
							if err := json.Unmarshal(raw, &forged.Desired); err != nil {
								t.Fatal(err)
							}
							forged.Desired.Sections[0].Items = append(forged.Desired.Sections[0].Items, item)
							if response := f.request(t, "POST", "/ui/settings/navigation/"+action, "alice", personalNavigationForm(t, forged)); response.Code != http.StatusBadRequest {
								t.Fatalf("forged %s: %d %s", action, response.Code, response.Body.String())
							}
						}
						if save == "edit-years" {
							years := roleIntentItem(t, &limited.Desired, "catalog:Years")
							years.Title, years.Titles = "Personal Years", nil
							limited.Renamed = append(limited.Renamed, yearsID)
						}
						preview := f.request(t, "POST", "/ui/settings/navigation/preview", "alice", personalNavigationForm(t, limited))
						if preview.Code != http.StatusOK || strings.Contains(preview.Body.String(), "Personal Classes") || strings.Contains(preview.Body.String(), "/ui/catalog/Classes") {
							t.Fatalf("restricted preview: %d %s", preview.Code, preview.Body.String())
						}
						if save != "none" {
							personalSave(t, f, "alice", limited)
						}
						switchRole(false)
						restored := f.personalEditor(t, "alice", sub)
						if save == "edit-years" {
							if roleIntentItem(t, &restored.Desired, "catalog:Years").Title != "Personal Years" {
								t.Fatal("visible edit lost")
							}
							// Normalize only the separately requested Years edit for comparison.
							years := roleIntentItem(t, &restored.Desired, "catalog:Years")
							original := roleIntentItem(t, &full.Desired, "catalog:Years")
							years.Title, years.Titles = original.Title, original.Titles
						}
						after, err := json.Marshal(restored.Desired)
						if err != nil {
							t.Fatal(err)
						}
						if string(before) != string(after) {
							t.Fatalf("%s intent lost after %s save:\nbefore %s\nafter %s", intent, save, before, after)
						}
						nav := roleIntentNav(t, f, "alice", sub)
						if (semanticFind(nav, "data-nav-id", classID) != nil) != (intent != "hide") {
							t.Fatal("runtime disagrees with retained hide intent")
						}
						if intent == "rename" {
							link := semanticFind(nav, "data-nav-id", classID)
							if link == nil || semanticAttr(link, "title") != "Personal Classes" {
								t.Fatal("runtime lost retained rename")
							}
						}
						if intent == "move" {
							destination := restored.Desired.Sections[len(restored.Desired.Sections)-1].ID
							if parent := semanticFind(nav, "data-nav-id", destination); parent == nil || semanticFind(parent, "data-nav-id", classID) == nil {
								t.Fatal("runtime lost retained placement")
							}
						}
						bobAfter := f.personalEditor(t, "bob", sub)
						rawBob, err := json.Marshal(bobAfter.Desired)
						if err != nil {
							t.Fatal(err)
						}
						if string(rawBob) != string(bobTree) || bobAfter.Revision != bobBefore.Revision {
							t.Fatal("Alice changed Bob's settings")
						}
						otherSub := "School"
						if sub != "" {
							otherSub = ""
						}
						if f.personalEditor(t, "alice", otherSub).Revision != "" {
							t.Fatal("context isolation lost")
						}
					})
				})
			}
		}
	}
}
