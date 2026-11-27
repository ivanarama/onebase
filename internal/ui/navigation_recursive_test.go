package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/navigation"
	"github.com/ivantit66/onebase/internal/realtime"
	"golang.org/x/net/html"
	"gopkg.in/yaml.v3"
)

const recursiveSchoolYAML = `sections:
- id: reports
  title: Reports
  nodes:
  - id: direct
    target: catalog:Classes
    title: First command
  - id: masters
    title: Masters
    nodes:
    - id: held
      title: Held
      nodes:
      - id: full
        title: Custom entry
        target: catalog:Classes
      - id: orders
        target: document:Orders
      - id: empty
        title: Empty private folder
        nodes:
        - id: forbidden
          target: catalog:Private
  - id: chief
    title: Chief only
    roles: [Chief]
    nodes:
    - id: chief-command
      target: processor:ClassJournal
  - id: last
    target: catalog:Classes
    title: Last command
`

func newRecursiveHTTPFixture(t *testing.T) navigationHTTPFixture {
	t.Helper()
	f := newNavigationHTTPFixture(t)
	var menu metadata.Menu
	if err := yaml.Unmarshal([]byte(recursiveSchoolYAML), &menu); err != nil {
		t.Fatal(err)
	}
	f.server.reg.GetSubsystem("School").Menu = &menu
	return f
}
func recursiveNavDocument(t *testing.T, f navigationHTTPFixture, login string) *html.Node {
	t.Helper()
	rec := f.request(t, "GET", "/ui/?subsystem=School", login, nil)
	if rec.Code != 200 {
		t.Fatalf("menu %d %s", rec.Code, rec.Body.String())
	}
	doc, err := html.Parse(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	return semanticFind(doc, "id", "ob-nav")
}
func TestRecursiveNavigationHTTPOrderAccessAndRoles(t *testing.T) {
	f := newRecursiveHTTPFixture(t)
	nav := recursiveNavDocument(t, f, "alice")
	for _, id := range []string{"cfg:reports", "cfg:masters", "cfg:held", "cfg:full", "cfg:last"} {
		if semanticFind(nav, "data-nav-id", id) == nil {
			t.Fatal("missing", id)
		}
	}
	for _, id := range []string{"cfg:empty", "cfg:forbidden", "cfg:chief", "cfg:chief-command"} {
		if semanticFind(nav, "data-nav-id", id) != nil {
			t.Fatal("restricted node", id)
		}
	}
	got := semanticLinks(semanticFind(nav, "data-nav-id", "cfg:reports"))
	want := []string{"/ui/catalog/Classes?subsystem=School", "/ui/catalog/Classes?subsystem=School", "/ui/document/Orders?subsystem=School", "/ui/catalog/Classes?subsystem=School"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatal("mixed order or duplicate entry lost", got)
	}
	if rec := f.request(t, "GET", "/ui/catalog/Private?subsystem=School", "alice", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("direct ACL: %d", rec.Code)
	}
	admin := recursiveNavDocument(t, f, "admin")
	if semanticFind(admin, "data-nav-id", "cfg:forbidden") == nil || semanticFind(admin, "data-nav-id", "cfg:chief-command") == nil {
		t.Fatal("admin lost permitted tree")
	}
	// Shared browser persistence is partitioned by authenticated user.
	key := semanticAttr(semanticFind(nav, "data-nav-id", "cfg:held"), "data-nav-key")
	bobKey := semanticAttr(semanticFind(recursiveNavDocument(t, f, "bob"), "data-nav-id", "cfg:held"), "data-nav-key")
	if key == "" || key == bobKey {
		t.Fatal("collapse key not user scoped")
	}
	user, err := f.server.authRepo.GetByLogin(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	role := &auth.Role{Name: "Chief"}
	if err = f.server.authRepo.SyncRoles(context.Background(), []*auth.Role{semanticTeacher().Roles[0], role}); err != nil {
		t.Fatal(err)
	}
	if err = f.server.authRepo.AssignRole(context.Background(), user.ID, role.ID); err != nil {
		t.Fatal(err)
	}
	if semanticFind(recursiveNavDocument(t, f, "alice"), "data-nav-id", "cfg:chief-command") == nil {
		t.Fatal("role union lost explicitly allowed branch")
	}
}
func TestRecursiveNavigationHTTPAdminAndPersonalLayers(t *testing.T) {
	f := newRecursiveHTTPFixture(t)
	state := f.editor(t, "School")
	nodes := state.Desired.Sections[0].Nodes
	// Reorder a folder between commands, then create a nested common folder.
	state.Desired.Sections[0].Nodes = []navigation.Node{nodes[1], nodes[0], nodes[2], nodes[3]}
	held := &state.Desired.Sections[0].Nodes[0].Nodes[0]
	full := held.Nodes[0]
	held.Nodes = append(held.Nodes[1:], navigation.Node{Item: navigation.Item{ID: "new:custom", Title: "Common folder"}, Nodes: []navigation.Node{full}})
	response := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, state))
	if response.Code != 303 {
		t.Fatalf("common save %d %s", response.Code, response.Body.String())
	}
	common := f.editor(t, "School")
	custom := common.Desired.Sections[0].Nodes[0].Nodes[0].Nodes[2]
	if !strings.HasPrefix(custom.ID, "adm:") || custom.Nodes[0].ID != "cfg:full" {
		t.Fatal("nested common allocation lost")
	}
	personal := decodeNavigationBootstrap(t, f.request(t, "GET", "/ui/settings/navigation?subsystem=School", "alice", nil))
	for _, id := range []string{"cfg:chief", "cfg:forbidden"} {
		if _, ok := navigationEditorNodes(personal.Base)[id]; ok {
			t.Fatal("private identity leaked", id)
		}
	}
	personal.Desired.Sections[0].Nodes[0].Nodes[0].Nodes[1].Title = "Personal folder"
	raw, _ := json.Marshal(personal.Desired)
	form := url.Values{"subsystem": {"School"}, "revision": {personal.Revision}, "base_revision": {personal.BaseRevision}, "renamed": {"[]"}, "desired": {string(raw)}}
	response = f.request(t, "POST", "/ui/settings/navigation/save", "alice", form)
	if response.Code != 303 {
		t.Fatalf("personal save %d %s", response.Code, response.Body.String())
	}
	if semanticFind(recursiveNavDocument(t, f, "alice"), "data-nav-id", custom.ID) == nil {
		t.Fatal("personal nested branch lost")
	}
	saved := decodeNavigationBootstrap(t, f.request(t, "GET", "/ui/settings/navigation?subsystem=School", "alice", nil))
	if !strings.Contains(string(mustJSONRecursive(t, saved.Desired)), "Personal folder") {
		t.Fatal("personal rename lost")
	}
	bob := decodeNavigationBootstrap(t, f.request(t, "GET", "/ui/settings/navigation?subsystem=School", "bob", nil))
	if strings.Contains(string(mustJSONRecursive(t, bob.Desired)), "Personal folder") {
		t.Fatal("personal override crossed user boundary")
	}
	// Reload and save without edits: no descendants are discarded.
	form.Set("revision", saved.Revision)
	form.Set("base_revision", saved.BaseRevision)
	form.Set("desired", string(mustJSONRecursive(t, saved.Desired)))
	if rec := f.request(t, "POST", "/ui/settings/navigation/save", "alice", form); rec.Code != 303 {
		t.Fatalf("round-trip %d %s", rec.Code, rec.Body.String())
	}
	after := decodeNavigationBootstrap(t, f.request(t, "GET", "/ui/settings/navigation?subsystem=School", "alice", nil))
	h1, _ := saved.Desired.Hash()
	h2, _ := after.Desired.Hash()
	if h1 != h2 {
		t.Fatal("round-trip changed tree")
	}
	response = f.request(t, "POST", "/ui/settings/navigation/reset", "alice", url.Values{"subsystem": {"School"}, "revision": {after.Revision}})
	if response.Code != 303 {
		t.Fatal("reset", response.Code)
	}
	reset := decodeNavigationBootstrap(t, f.request(t, "GET", "/ui/settings/navigation?subsystem=School", "alice", nil))
	if strings.Contains(string(mustJSONRecursive(t, reset.Desired)), "Personal folder") {
		t.Fatal("reset did not restore common tree")
	}
}
func mustJSONRecursive(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Optional native-browser acceptance; compilation completes before Chrome starts.
// The fixture uses only its own SQLite and real authenticated HTTP sessions.
func TestRecursiveNavigationBrowser(t *testing.T) {
	if os.Getenv("OB_RECURSIVE_MENU_BROWSER") != "1" {
		t.Skip("set OB_RECURSIVE_MENU_BROWSER=1 and OB_BROWSER_CHROME for native acceptance")
	}
	f := newRecursiveHTTPFixture(t)
	if err := f.server.store.Migrate(context.Background(), f.server.reg.Entities()); err != nil {
		t.Fatal(err)
	}
	f.server.hub = realtime.NewHub()
	server := httptest.NewServer(f.http)
	defer func() { f.server.CloseEventStreams(); server.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	python := os.Getenv("OB_BROWSER_PYTHON")
	if python == "" {
		python = "python"
	}
	cmd := exec.CommandContext(ctx, python, filepath.Join("testdata", "recursive_navigation_browser.py")) //nolint:gosec // Optional test-only local Python; fixed script and authenticated loopback fixture.
	artifacts := os.Getenv("OB_RECURSIVE_MENU_ARTIFACTS")
	if artifacts == "" {
		artifacts = t.TempDir()
	}
	cmd.Env = append(os.Environ(), "OB_RECURSIVE_MENU_URL="+server.URL, "OB_RECURSIVE_MENU_ALICE="+f.cookie["alice"].Value, "OB_RECURSIVE_MENU_BOB="+f.cookie["bob"].Value, "OB_RECURSIVE_MENU_ARTIFACTS="+artifacts)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatal("recursive native acceptance: ", err)
	}
}

func TestRecursiveNavigationHTTPRoleClausesSurviveMovedCommand(t *testing.T) {
	f := newRecursiveHTTPFixture(t)
	state := f.editor(t, "School")
	command := state.Desired.Sections[0].Nodes[2].Nodes[0]
	command.RoleClauses = nil // Client metadata is never authoritative.
	state.Desired.Sections[0].Nodes[2].Nodes = nil
	state.Desired.Sections[0].Nodes = append(state.Desired.Sections[0].Nodes, command)
	rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, state))
	if rec.Code != 303 {
		t.Fatalf("move %d %s", rec.Code, rec.Body.String())
	}
	if semanticFind(recursiveNavDocument(t, f, "alice"), "data-nav-id", "cfg:chief-command") != nil {
		t.Fatal("moving command removed inherited role restriction")
	}
	if semanticFind(recursiveNavDocument(t, f, "admin"), "data-nav-id", "cfg:chief-command") == nil {
		t.Fatal("admin command disappeared")
	}
	// The role's declared subsystem composition still narrows every depth.
	f.server.reg.GetSubsystem("School").HomePage = &metadata.HomePage{NavByRole: map[string]*metadata.SubsystemContents{"Teacher": {Catalogs: []string{"Classes", "NotInSubsystem"}}}}
	nav := recursiveNavDocument(t, f, "alice")
	if semanticFind(nav, "data-nav-id", "cfg:full") == nil || semanticFind(nav, "data-nav-id", "cfg:orders") != nil {
		t.Fatal("recursive nav_by_role projection lost")
	}
	personal := f.personalEditor(t, "alice", "School")
	forged := personal
	forged.Desired = f.editor(t, "School").Desired
	if rec := f.request(t, "POST", "/ui/settings/navigation/save", "alice", personalNavigationForm(t, forged)); rec.Code != 400 {
		t.Fatalf("forged role composition accepted %d", rec.Code)
	}
}

func TestRecursiveNavigationHTTPRevokedAncestorKeepsPrivateIntent(t *testing.T) {
	f := newRecursiveHTTPFixture(t)
	ctx := context.Background()
	f.server.reg.GetSubsystem("School").Menu.Sections = append(f.server.reg.GetSubsystem("School").Menu.Sections, metadata.MenuSection{ID: "private-root", Title: "PRIVATE_ROOT_SENTINEL", Nodes: []metadata.MenuNode{{ID: "private-middle", Title: "Private middle", Nodes: []metadata.MenuNode{{ID: "private-leaf", Target: "catalog:Private"}}}}})
	grant := &auth.Role{Name: "PrivateGrant", Permissions: auth.Permission{Catalogs: map[string][]string{"Private": {"read"}}}}
	if err := f.server.authRepo.SyncRoles(ctx, []*auth.Role{semanticTeacher().Roles[0], grant}); err != nil {
		t.Fatal(err)
	}
	alice, err := f.server.authRepo.GetByLogin(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.server.authRepo.AssignRole(ctx, alice.ID, grant.ID); err != nil {
		t.Fatal(err)
	}
	state := f.personalEditor(t, "alice", "School")
	root := &state.Desired.Sections[0]
	public := root.Nodes[0]
	root.Nodes = root.Nodes[1:]
	var middle navigation.Node
	for i := range state.Desired.Sections {
		section := &state.Desired.Sections[i]
		if section.ID == "cfg:private-root" {
			middle = section.Nodes[0]
			section.Nodes = []navigation.Node{{Item: navigation.Item{ID: "new:hidden", Title: "HIDDEN_PERSONAL_SENTINEL"}}}
		}
	}
	middle.Nodes = append(middle.Nodes, public)
	root.Nodes = append(root.Nodes, middle)
	personalSave(t, f, "alice", state)
	if err = f.server.authRepo.UnassignRole(ctx, alice.ID, grant.ID); err != nil {
		t.Fatal(err)
	}
	state = f.personalEditor(t, "alice", "School")
	raw := string(mustJSONRecursive(t, state))
	for _, sentinel := range []string{"cfg:private-root", "PRIVATE_ROOT_SENTINEL", "HIDDEN_PERSONAL_SENTINEL", "cfg:private-leaf", "catalog:Private"} {
		if strings.Contains(raw, sentinel) {
			t.Fatal("private metadata leaked", sentinel)
		}
	}
	state.Desired.Sections[0].Title = "Visible rename"
	personalSave(t, f, "alice", state)
	// Regrant must restore private siblings and their independent hidden intent.
	if err = f.server.authRepo.AssignRole(ctx, alice.ID, grant.ID); err != nil {
		t.Fatal(err)
	}
	state = f.personalEditor(t, "alice", "School")
	raw = string(mustJSONRecursive(t, state.Desired))
	for _, sentinel := range []string{"cfg:private-leaf", "HIDDEN_PERSONAL_SENTINEL", "Visible rename"} {
		if !strings.Contains(raw, sentinel) {
			t.Fatal("private intent lost after regrant", sentinel)
		}
	}
}
