package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/navigation"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/websec"
)

const personalSchool = "ОбразовательнаяДеятельность"

func schoolNavigationFixture(t *testing.T, db *storage.DB, dir string, seed bool) navigationHTTPFixture {
	t.Helper()
	ctx := context.Background()
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(proj.Close)
	for _, err := range []error{db.EnsureServiceSchema(ctx), db.Migrate(ctx, proj.Entities), db.MigrateInfoRegisters(ctx, proj.InfoRegisters), db.EnsureAuditSchema(ctx)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: proj.Entities, Programs: proj.Programs, ManagerPrograms: proj.ManagerPrograms, Registers: proj.Registers, InfoRegs: proj.InfoRegisters, Constants: proj.Constants, Enums: proj.Enums, Reports: proj.Reports})
	reg.LoadModules(proj.Modules)
	reg.LoadProcessors(proj.Processors)
	reg.LoadSubsystems(proj.Subsystems)
	reg.LoadHomePage(proj.HomePage)
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnsureRolesSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if seed {
		roles, err := auth.LoadRolesYAML(filepath.Join(dir, "roles"))
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.SyncRoles(ctx, roles); err != nil {
			t.Fatal(err)
		}
		for _, login := range []string{"admin", "alice", "bob"} {
			user, err := repo.Create(ctx, login, "secret123", login, login == "admin")
			if err != nil {
				t.Fatal(err)
			}
			if login != "admin" {
				if err := repo.AssignRole(ctx, user.ID, roles[0].ID); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	cookies := map[string]*http.Cookie{}
	for _, login := range []string{"admin", "alice", "bob"} {
		user, err := repo.GetByLogin(ctx, login)
		if err != nil || user == nil {
			t.Fatalf("user %s: %v", login, err)
		}
		token, err := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
		if err != nil {
			t.Fatal(err)
		}
		cookies[login] = &http.Cookie{Name: "onebase_session", Value: token}
	}
	bundle, err := i18n.Load(i18n.EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg, db, interpreter.New(), repo, Config{Bundle: bundle, Lang: "ru"}, nil)
	t.Cleanup(s.Close)
	router := chi.NewRouter()
	router.Use(websec.CSRFProtect)
	s.MountStatic(router)
	router.Group(func(group chi.Router) { group.Use(repo.Middleware); s.Mount(group) })
	plain := chi.NewRouter()
	plain.Use(websec.CSRFProtect)
	s.Mount(plain)
	return navigationHTTPFixture{s, router, plain, cookies}
}

func (f navigationHTTPFixture) personalEditor(t *testing.T, login, sub string) navigationBootstrap {
	t.Helper()
	return decodeNavigationBootstrap(t, f.request(t, "GET", "/ui/settings/navigation?"+url.Values{"subsystem": {sub}}.Encode(), login, nil))
}

func personalNavigationForm(t *testing.T, state navigationBootstrap) url.Values {
	t.Helper()
	form := navigationForm(t, state)
	raw, err := json.Marshal(state.Renamed)
	if err != nil {
		t.Fatal(err)
	}
	form.Set("base_revision", state.BaseRevision)
	form.Set("renamed", string(raw))
	return form
}

func personalSave(t *testing.T, f navigationHTTPFixture, login string, state navigationBootstrap) {
	t.Helper()
	if rec := f.request(t, "POST", "/ui/settings/navigation/save", login, personalNavigationForm(t, state)); rec.Code != http.StatusSeeOther {
		t.Fatalf("save %s: %d %s", login, rec.Code, rec.Body.String())
	}
}

func personalSection(t *testing.T, state *navigationBootstrap, id string) *navigation.Section {
	t.Helper()
	for i := range state.Desired.Sections {
		if state.Desired.Sections[i].ID == id {
			return &state.Desired.Sections[i]
		}
	}
	t.Fatalf("missing section %s", id)
	return nil
}

func TestPersonalNavigation_SchoolHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := schoolNavigationFixture(t, db, "testdata/navigation-school", true)
		common := f.editor(t, personalSchool)
		personalSection(t, &common, "cfg:academic-years").Title = "Common years"
		personalSection(t, &common, "cfg:academic-years").Titles = nil
		commonSection := personalSection(t, &common, "cfg:academic-years")
		commonSection.Groups = append(commonSection.Groups, navigation.Group{ID: "new:shared-empty", Title: "Shared empty folder"})
		if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, common)); rec.Code != 303 {
			t.Fatal(rec.Code, rec.Body.String())
		}
		alice := f.personalEditor(t, "alice", personalSchool)
		shared := personalSection(t, &alice, "cfg:academic-years").Groups[3]
		if !strings.HasPrefix(shared.ID, "adm:") || alice.Origins[shared.ID] != "common" {
			t.Fatal("inherited empty administrator folder missing")
		}
		if alice.Origins["cfg:academic-years"] != "common" {
			t.Fatal("common provenance lost")
		}
		section := personalSection(t, &alice, "cfg:academic-years")
		item := section.Groups[0].Items[0]
		section.Groups[0].Items = nil
		section.Groups = append(section.Groups, navigation.Group{ID: "new:alice", Title: "Alice folder", Items: []navigation.Item{item}})
		section.Title, section.Titles = "Alice years", nil
		alice.Renamed = []string{section.ID}
		preview := f.request(t, "POST", "/ui/settings/navigation/preview", "alice", personalNavigationForm(t, alice))
		if preview.Code != 200 || !strings.Contains(preview.Body.String(), "Alice folder") {
			t.Fatal(preview.Code, preview.Body.String())
		}
		if f.personalEditor(t, "alice", personalSchool).Revision != "" {
			t.Fatal("preview wrote state")
		}
		personalSave(t, f, "alice", alice)
		saved := f.personalEditor(t, "alice", personalSchool)
		savedSection := personalSection(t, &saved, "cfg:academic-years")
		folder := savedSection.Groups[len(savedSection.Groups)-1]
		if !strings.HasPrefix(folder.ID, "usr:") || folder.Items[0].ID != item.ID {
			t.Fatal("personal identity/move lost")
		}
		bob := f.personalEditor(t, "bob", personalSchool)
		if personalSection(t, &bob, "cfg:academic-years").Title != "Common years" || strings.Contains(f.request(t, "GET", "/ui/?subsystem="+url.QueryEscape(personalSchool), "bob", nil).Body.String(), "Alice folder") {
			t.Fatal("account isolation lost")
		}
		personalSection(t, &bob, "cfg:academic-years").Title = "Bob years"
		bob.Renamed = []string{"cfg:academic-years"}
		personalSave(t, f, "bob", bob)
		// Personal settings must not affect either administrator or global context.
		if f.editor(t, personalSchool).Desired.Sections[0].Title != "Common years" || f.personalEditor(t, "alice", "").Revision != "" {
			t.Fatal("layer/context isolation lost")
		}
		page := f.request(t, "GET", "/ui/?subsystem="+url.QueryEscape(personalSchool), "alice", nil)
		if !strings.Contains(page.Body.String(), "Alice folder") || !strings.Contains(page.Body.String(), "/ui/settings/navigation?") {
			t.Fatal("runtime or fixed entry missing")
		}
		// Reset only Alice: Bob and common remain unchanged.
		if rec := f.request(t, "POST", "/ui/settings/navigation/reset", "alice", url.Values{"subsystem": {personalSchool}, "revision": {saved.Revision}}); rec.Code != 303 {
			t.Fatal(rec.Code)
		}
		reset := f.personalEditor(t, "alice", personalSchool)
		if reset.Revision != "" || personalSection(t, &reset, "cfg:academic-years").Title != "Common years" {
			t.Fatal("personal reset did not inherit common")
		}
		bob = f.personalEditor(t, "bob", personalSchool)
		if personalSection(t, &bob, "cfg:academic-years").Title != "Bob years" {
			t.Fatal("reset altered Bob")
		}
		common = f.editor(t, personalSchool)
		if rec := f.request(t, "POST", "/ui/admin/navigation/reset", "admin", url.Values{"subsystem": {personalSchool}, "revision": {common.Revision}}); rec.Code != 303 {
			t.Fatal(rec.Code)
		}
		reset = f.personalEditor(t, "alice", personalSchool)
		if personalSection(t, &reset, "cfg:academic-years").Title != "Учебные годы" {
			t.Fatal("admin reset did not inherit YAML")
		}
		bob = f.personalEditor(t, "bob", personalSchool)
		if personalSection(t, &bob, "cfg:academic-years").Title != "Bob years" {
			t.Fatal("admin reset erased personal override")
		}
		entries, err := db.AuditSearch(context.Background(), storage.AuditFilter{EntityName: "subsystem:" + personalSchool}, 100, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, entry := range entries {
			if strings.HasPrefix(entry.Action, "navigation.user.") {
				found++
				raw, _ := json.Marshal(entry)
				if entry.UserID == "" || entry.UserLogin == "" || strings.Contains(string(raw), "Alice years") || strings.Contains(string(raw), "cfg:") {
					t.Fatal("unsafe audit", string(raw))
				}
			}
		}
		if found != 3 {
			t.Fatal("personal audit count", found)
		}
	})
}

func TestPersonalNavigation_PrivacyForgedTargetsAndConflictMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := schoolNavigationFixture(t, db, "testdata/navigation-school", true)
		checkPrivate := func(body string) {
			t.Helper()
			for _, denied := range []string{"ClosedSentinel", "SECRET_", "closed-sentinel"} {
				if strings.Contains(body, denied) {
					t.Fatal("private metadata in response", denied)
				}
			}
		}
		rec := f.request(t, "GET", "/ui/settings/navigation?subsystem="+url.QueryEscape(personalSchool), "alice", nil)
		checkPrivate(rec.Body.String())
		alice := decodeNavigationBootstrap(t, rec)
		admin := f.editor(t, personalSchool)
		// Closed metadata or an admin-hidden item cannot be reintroduced by ID.
		for _, forbidden := range []navigation.Section{*personalSection(t, &admin, "cfg:closed-sentinel-section")} {
			forged := f.personalEditor(t, "alice", personalSchool)
			forged.Desired.Sections = append(forged.Desired.Sections, forbidden)
			if got := f.request(t, "POST", "/ui/settings/navigation/save", "alice", personalNavigationForm(t, forged)); got.Code != 400 {
				t.Fatal("forged private tree", got.Code)
			}
		}
		for _, key := range []string{"login", "key", "layer", "ops"} {
			form := personalNavigationForm(t, alice)
			form.Set(key, "admin")
			if got := f.request(t, "POST", "/ui/settings/navigation/save", "alice", form); got.Code != 400 {
				t.Fatal("forged scope", key, got.Code)
			}
		}
		for _, action := range []string{"save", "preview", "reset"} {
			form := personalNavigationForm(t, alice)
			if action == "reset" {
				form = url.Values{"revision": {alice.Revision}}
			}
			form.Set("subsystem", "ClosedOnly")
			if got := f.request(t, "POST", "/ui/settings/navigation/"+action+"?subsystem="+url.QueryEscape(personalSchool), "alice", form); got.Code != 403 {
				t.Fatal("POST body subsystem gate", action, got.Code)
			}
		}
		if got := f.request(t, "GET", "/ui/settings/navigation?subsystem=ClosedOnly", "alice", nil); got.Code != 403 {
			t.Fatal("GET subsystem gate", got.Code)
		}
		// Hide bells in common, then try to paste the earlier personal bootstrap.
		personalSection(t, &admin, "cfg:academic-years").Groups[1].Items = personalSection(t, &admin, "cfg:academic-years").Groups[1].Items[1:]
		if got := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, admin)); got.Code != 303 {
			t.Fatal(got.Code)
		}
		if got := f.request(t, "POST", "/ui/settings/navigation/save", "alice", personalNavigationForm(t, alice)); got.Code != 409 {
			t.Fatal("stale common base", got.Code)
		}
		current := f.personalEditor(t, "alice", personalSchool)
		forged := current
		forged.Desired = alice.Desired
		if got := f.request(t, "POST", "/ui/settings/navigation/save", "alice", personalNavigationForm(t, forged)); got.Code != 400 {
			t.Fatal("admin-hidden restoration", got.Code)
		}
		if _, present := navigationEditorNodes(current.Base)["cfg:bells"]; present {
			t.Fatal("hidden item in palette")
		}
		if got := f.request(t, "GET", "/ui/inforeg/"+url.PathEscape(strings.ToLower("РасписаниеЗвонков")), "alice", nil); got.Code != 200 {
			t.Fatal("menu hiding changed ACL", got.Code)
		}
		first := current
		// Desired slices are aliased; reload the stale tab independently.
		second := f.personalEditor(t, "alice", personalSchool)
		personalSection(t, &first, "cfg:academic-years").Title = "Winner"
		first.Renamed = []string{"cfg:academic-years"}
		personalSave(t, f, "alice", first)
		personalSection(t, &second, "cfg:academic-years").Title = "Loser"
		second.Renamed = []string{"cfg:academic-years"}
		conflict := f.request(t, "POST", "/ui/settings/navigation/save", "alice", personalNavigationForm(t, second))
		if conflict.Code != 409 {
			t.Fatal("stale own CAS", conflict.Code)
		}
		winner := decodeNavigationBootstrap(t, conflict)
		if personalSection(t, &winner, "cfg:academic-years").Title != "Winner" {
			t.Fatal("winner lost")
		}
		checkPrivate(conflict.Body.String())
		if got := f.request(t, "POST", "/ui/settings/navigation/reset", "alice", url.Values{"subsystem": {personalSchool}, "revision": {second.Revision}}); got.Code != 409 {
			t.Fatal("reset CAS", got.Code)
		}
	})
}

func TestPersonalNavigation_ExplicitNeutralRenamesAndRoleChangesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := schoolNavigationFixture(t, db, "testdata/navigation-school", true)
		admin := f.editor(t, personalSchool)
		section := personalSection(t, &admin, "cfg:academic-years")
		section.Title, section.Titles = "Common", nil
		section.Groups[0].Title = "Common group"
		section.Groups[0].Items[0].Title = "Common item"
		if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, admin)); rec.Code != 303 {
			t.Fatal(rec.Code)
		}
		alice := f.personalEditor(t, "alice", personalSchool)
		alice.Renamed = []string{"cfg:academic-years", "cfg:periods", "cfg:periods-list"}
		personal := personalSection(t, &alice, "cfg:academic-years")
		privateItem := personal.Groups[0].Items[0]
		accessibleAnchor := personal.Items[0]
		personal.Items = personal.Items[1:]
		personal.Groups[0].Items = nil
		personal.Groups = append(personal.Groups, navigation.Group{ID: "new:retained-private", Title: "My private placement", Items: []navigation.Item{accessibleAnchor, privateItem}})
		personalSave(t, f, "alice", alice) // Explicit away/back-to-same text, no tree difference.
		alice = f.personalEditor(t, "alice", personalSchool)
		personalSection(t, &alice, "cfg:academic-years").Icon = "book-open"
		personalSave(t, f, "alice", alice) // Neutral intent survives unrelated resave.
		// Revoke Alice's period permission without restarting or changing Bob.
		teacher, err := auth.LoadRoleFile("testdata/navigation-school/roles/Teacher.yaml")
		if err != nil {
			t.Fatal(err)
		}
		limited := *teacher
		limited.Name = "LimitedTeacher"
		limited.Permissions.Catalogs = map[string][]string{"УчебныеГоды": {"read"}, "Классы": {"read"}}
		if err := f.server.authRepo.SyncRoles(context.Background(), []*auth.Role{&limited}); err != nil {
			t.Fatal(err)
		}
		roles, err := f.server.authRepo.ListRoles(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var teacherID string
		for _, role := range roles {
			if role.Name == "Teacher" {
				teacherID = role.ID
			}
		}
		user, err := f.server.authRepo.GetByLogin(context.Background(), "alice")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.server.authRepo.AssignRole(context.Background(), user.ID, limited.ID); err != nil {
			t.Fatal(err)
		}
		if err := f.server.authRepo.UnassignRole(context.Background(), user.ID, teacherID); err != nil {
			t.Fatal(err)
		}
		limitedState := f.personalEditor(t, "alice", personalSchool)
		if _, visible := navigationEditorNodes(limitedState.Desired)["cfg:periods-list"]; visible {
			t.Fatal("revoked item in editor")
		}
		if rec := f.request(t, "GET", "/ui/catalog/"+url.PathEscape(strings.ToLower("ПериодыОбучения")), "alice", nil); rec.Code != 403 {
			t.Fatal("revoked direct URL", rec.Code)
		}
		personalSection(t, &limitedState, "cfg:academic-years").Icon = "calendar-days"
		personalSave(t, f, "alice", limitedState)
		admin = f.editor(t, personalSchool)
		section = personalSection(t, &admin, "cfg:academic-years")
		section.Title = "New common"
		section.Groups[0].Title = "New group"
		section.Groups[0].Items[0].Title = "New item"
		if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, admin)); rec.Code != 303 {
			t.Fatal(rec.Code)
		}
		if err := f.server.authRepo.AssignRole(context.Background(), user.ID, teacherID); err != nil {
			t.Fatal(err)
		}
		alice = f.personalEditor(t, "alice", personalSchool)
		section = personalSection(t, &alice, "cfg:academic-years")
		if section.Title != "Common" || section.Groups[0].Title != "Common group" || len(section.Groups[len(section.Groups)-1].Items) != 2 || section.Groups[len(section.Groups)-1].Items[1].ID != "cfg:periods-list" || section.Groups[len(section.Groups)-1].Items[1].Title != "Common item" {
			t.Fatal("explicit/private intent lost", section)
		}
		bob := f.personalEditor(t, "bob", personalSchool)
		if personalSection(t, &bob, "cfg:academic-years").Title != "New common" {
			t.Fatal("Bob did not inherit common change")
		}
	})
}

func personalRevokePeriods(t *testing.T, f navigationHTTPFixture) func() {
	t.Helper()
	ctx := context.Background()
	teacher, err := auth.LoadRoleFile("testdata/navigation-school/roles/Teacher.yaml")
	if err != nil {
		t.Fatal(err)
	}
	limited := *teacher
	limited.Name = "LimitedTeacher"
	limited.Permissions.Catalogs = map[string][]string{"УчебныеГоды": {"read"}, "Классы": {"read"}}
	if err := f.server.authRepo.SyncRoles(ctx, []*auth.Role{&limited}); err != nil {
		t.Fatal(err)
	}
	roles, err := f.server.authRepo.ListRoles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var teacherID string
	for _, role := range roles {
		if role.Name == "Teacher" {
			teacherID = role.ID
		}
	}
	user, err := f.server.authRepo.GetByLogin(ctx, "alice")
	if err != nil || user == nil || teacherID == "" {
		t.Fatalf("role fixture: %v, teacher=%s", err, teacherID)
	}
	if err := f.server.authRepo.AssignRole(ctx, user.ID, limited.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.server.authRepo.UnassignRole(ctx, user.ID, teacherID); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := f.server.authRepo.AssignRole(ctx, user.ID, teacherID); err != nil {
			t.Fatal(err)
		}
	}
}

func personalGroupByID(t *testing.T, state *navigationBootstrap, id string) *navigation.Group {
	t.Helper()
	for i := range state.Desired.Sections {
		for j := range state.Desired.Sections[i].Groups {
			group := &state.Desired.Sections[i].Groups[j]
			if group.ID == id {
				return group
			}
		}
	}
	t.Fatalf("missing personal group %s", id)
	return nil
}

func TestPersonalNavigation_ClosedParentPreservesPersonalFolder(t *testing.T) {
	for _, withPrivateItem := range []bool{false, true} {
		name := "empty-folder"
		if withPrivateItem {
			name = "folder-with-revoked-item"
		}
		t.Run(name, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				f := schoolNavigationFixture(t, db, "testdata/navigation-school", true)
				ctx := context.Background()
				admin := f.editor(t, personalSchool)
				visible := personalSection(t, &admin, "cfg:academic-years")
				visible.Groups = append(visible.Groups, navigation.Group{ID: "new:shared-empty", Title: "Shared empty folder"})
				if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, admin)); rec.Code != 303 {
					t.Fatal("common folder", rec.Code)
				}
				grant := &auth.Role{Name: "TemporaryClosedAccess"}
				grant.Permissions.Catalogs = map[string][]string{"ClosedSentinel": {"read"}}
				if err := f.server.authRepo.SyncRoles(ctx, []*auth.Role{grant}); err != nil {
					t.Fatal(err)
				}
				alice, err := f.server.authRepo.GetByLogin(ctx, "alice")
				if err != nil || alice == nil {
					t.Fatal(err)
				}
				if err := f.server.authRepo.AssignRole(ctx, alice.ID, grant.ID); err != nil {
					t.Fatal(err)
				}
				state := f.personalEditor(t, "alice", personalSchool)
				closed := personalSection(t, &state, "cfg:closed-sentinel-section")
				folder := navigation.Group{ID: "new:closed-parent-folder", Title: "Private personal folder"}
				if withPrivateItem {
					folder.Items = closed.Groups[0].Items
					closed.Groups[0].Items = nil
				}
				closed.Groups = append(closed.Groups, folder)
				visible = personalSection(t, &state, "cfg:academic-years")
				visible.Groups = append(visible.Groups, navigation.Group{ID: "new:visible-empty", Title: "Visible personal empty folder"})
				state.Desired.Sections = append(state.Desired.Sections, navigation.Section{ID: "new:personal-empty-section", Title: "Personal empty section"})
				personalSave(t, f, "alice", state)
				state = f.personalEditor(t, "alice", personalSchool)
				closed = personalSection(t, &state, "cfg:closed-sentinel-section")
				folderID := closed.Groups[len(closed.Groups)-1].ID
				if !strings.HasPrefix(folderID, "usr:") {
					t.Fatal("personal identity missing", folderID)
				}
				if err := f.server.authRepo.UnassignRole(ctx, alice.ID, grant.ID); err != nil {
					t.Fatal(err)
				}
				if rec := f.request(t, "GET", "/ui/catalog/closedsentinel", "alice", nil); rec.Code != 403 {
					t.Fatal("revoked direct URL", rec.Code)
				}
				admin = f.editor(t, personalSchool)
				personalSection(t, &admin, "cfg:closed-sentinel-section").Title = "NEW_PRIVATE_SECTION_SENTINEL"
				personalSection(t, &admin, "cfg:closed-sentinel-section").Groups[0].Title = "NEW_PRIVATE_GROUP_SENTINEL"
				if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, admin)); rec.Code != 303 {
					t.Fatal("common rename", rec.Code)
				}
				checkPrivate := func(rec *httptest.ResponseRecorder) {
					t.Helper()
					if rec.Code != 200 {
						t.Error("private response status", rec.Code)
					}
					for _, token := range []string{"NEW_PRIVATE_", "SECRET_", "closed-sentinel", "ClosedSentinel", folderID, "Private personal folder"} {
						if strings.Contains(rec.Body.String(), token) {
							t.Error("closed metadata leaked", token)
						}
					}
				}
				checkPrivate(f.request(t, "GET", "/ui/settings/navigation?subsystem="+url.QueryEscape(personalSchool), "alice", nil))
				checkPrivate(f.request(t, "GET", "/ui?subsystem="+url.QueryEscape(personalSchool), "alice", nil))
				state = f.personalEditor(t, "alice", personalSchool)
				assertEmptyFolders := func(state navigationBootstrap) {
					t.Helper()
					var shared, personal, ownSection bool
					for _, section := range state.Desired.Sections {
						if strings.HasPrefix(section.ID, "usr:") && section.Title == "Personal empty section" {
							ownSection = true
						}
						for _, group := range section.Groups {
							shared = shared || strings.HasPrefix(group.ID, "adm:") && group.Title == "Shared empty folder" && len(group.Items) == 0
							personal = personal || strings.HasPrefix(group.ID, "usr:") && group.Title == "Visible personal empty folder" && len(group.Items) == 0
						}
					}
					if !shared || !personal || !ownSection {
						t.Error("accessible empty folders lost", shared, personal, ownSection)
					}
				}
				assertEmptyFolders(state)
				personalSection(t, &state, "cfg:academic-years").Icon = "book-open"
				checkPrivate(f.request(t, "POST", "/ui/settings/navigation/preview", "alice", personalNavigationForm(t, state)))
				if rec := f.request(t, "POST", "/ui/settings/navigation/save", "alice", personalNavigationForm(t, state)); rec.Code != 303 {
					t.Errorf("visible save after revoke: %d", rec.Code)
				}
				assertEmptyFolders(f.personalEditor(t, "alice", personalSchool))
				if err := f.server.authRepo.AssignRole(ctx, alice.ID, grant.ID); err != nil {
					t.Fatal(err)
				}
				state = f.personalEditor(t, "alice", personalSchool)
				closed = personalSection(t, &state, "cfg:closed-sentinel-section")
				if closed.Title != "NEW_PRIVATE_SECTION_SENTINEL" || personalSection(t, &state, "cfg:academic-years").Icon != "book-open" {
					t.Error("common title or visible edit lost")
				}
				restored := personalGroupByID(t, &state, folderID)
				if restored.Title != "Private personal folder" || withPrivateItem && (len(restored.Items) != 1 || restored.Items[0].ID != "cfg:closed-sentinel-item") || !withPrivateItem && len(restored.Items) != 0 {
					t.Error("private folder intent lost after regrant", restored)
				}
				assertEmptyFolders(state)
			})
		})
	}
}

func TestPersonalNavigation_VisiblePlacementInsideRevokedParent(t *testing.T) {
	for _, scenario := range []string{"usr-folder", "section-items", "inherited-group", "moved-inherited-group", "hide-moved-group", "move-visible-out", "admin-hidden-anchor", "neutral-parent-rename"} {
		t.Run(scenario, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				f := schoolNavigationFixture(t, db, "testdata/navigation-school", true)
				ctx := context.Background()
				grant := &auth.Role{Name: "TemporaryClosedAccess"}
				grant.Permissions.Catalogs = map[string][]string{"ClosedSentinel": {"read"}}
				if err := f.server.authRepo.SyncRoles(ctx, []*auth.Role{grant}); err != nil {
					t.Fatal(err)
				}
				alice, err := f.server.authRepo.GetByLogin(ctx, "alice")
				if err != nil || alice == nil {
					t.Fatal(err)
				}
				if err := f.server.authRepo.AssignRole(ctx, alice.ID, grant.ID); err != nil {
					t.Fatal(err)
				}
				state := f.personalEditor(t, "alice", personalSchool)
				academic := personalSection(t, &state, "cfg:academic-years")
				public := academic.Items[0]
				academic.Items = academic.Items[1:]
				closed := personalSection(t, &state, "cfg:closed-sentinel-section")
				private := closed.Groups[0].Items[0]
				moved := scenario == "moved-inherited-group" || scenario == "hide-moved-group"
				switch scenario {
				case "section-items":
					closed.Items = append(closed.Items, public)
				case "inherited-group", "moved-inherited-group", "hide-moved-group", "move-visible-out", "admin-hidden-anchor":
					closed.Groups[0].Items = append(closed.Groups[0].Items, public)
					if moved {
						group := closed.Groups[0]
						closed.Groups = nil
						state.Desired.Sections = append(state.Desired.Sections, navigation.Section{ID: "new:visible-root", Title: "Visible custom section", Groups: []navigation.Group{group}})
						closed = personalSection(t, &state, "cfg:closed-sentinel-section")
					}
				default:
					closed.Groups = append(closed.Groups, navigation.Group{ID: "new:visible-folder", Title: "Visible personal folder", Items: []navigation.Item{public}})
				}
				closed.Groups = append(closed.Groups, navigation.Group{ID: "new:hidden-intent", Title: "Hidden intent folder"})
				personalSave(t, f, "alice", state)
				state = f.personalEditor(t, "alice", personalSchool)
				var privateFolderID, publicFolderID string
				for _, section := range state.Desired.Sections {
					for _, group := range section.Groups {
						if group.Title == "Hidden intent folder" {
							privateFolderID = group.ID
						}
						if group.Title == "Visible personal folder" {
							publicFolderID = group.ID
						}
					}
				}
				if !strings.HasPrefix(privateFolderID, "usr:") {
					t.Fatal("private intent identity missing")
				}
				if err := f.server.authRepo.UnassignRole(ctx, alice.ID, grant.ID); err != nil {
					t.Fatal(err)
				}
				if rec := f.request(t, "GET", "/ui/catalog/closedsentinel", "alice", nil); rec.Code != 403 {
					t.Fatal("closed direct URL", rec.Code)
				}
				if rec := f.request(t, "GET", "/ui/catalog/"+url.PathEscape(strings.ToLower("УчебныеГоды")), "alice", nil); rec.Code != 200 {
					t.Fatal("accessible direct URL", rec.Code)
				}
				admin := f.editor(t, personalSchool)
				closed = personalSection(t, &admin, "cfg:closed-sentinel-section")
				closed.Title = "NEW_PARENT_SENTINEL"
				closed.Groups[0].Title = "New common group"
				if scenario == "admin-hidden-anchor" {
					closed.Groups[0].Items = nil
				}
				if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, admin)); rec.Code != 303 {
					t.Fatal("common rename", rec.Code)
				}
				checkPrivate := func(rec *httptest.ResponseRecorder) {
					t.Helper()
					if rec.Code != 200 {
						t.Error("permitted response", rec.Code)
					}
					for _, token := range []string{"ClosedSentinel", "cfg:closed-sentinel-item", "SECRET_METADATA_SENTINEL"} {
						if strings.Contains(rec.Body.String(), token) {
							t.Error("closed target leaked", token)
						}
					}
					if moved {
						for _, token := range []string{"cfg:closed-sentinel-section", "NEW_PARENT_SENTINEL", privateFolderID, "Hidden intent folder"} {
							if strings.Contains(rec.Body.String(), token) {
								t.Error("closed original ancestor leaked", token)
							}
						}
					}
				}
				checkPrivate(f.request(t, "GET", "/ui/settings/navigation?subsystem="+url.QueryEscape(personalSchool), "alice", nil))
				checkPrivate(f.request(t, "GET", "/ui?subsystem="+url.QueryEscape(personalSchool), "alice", nil))
				// The server may know extra ancestors internally, but they and
				// closed targets must remain unavailable to client input.
				for _, action := range []string{"preview", "save"} {
					forged := f.personalEditor(t, "alice", personalSchool)
					academic = personalSection(t, &forged, "cfg:academic-years")
					academic.Items = append(academic.Items, private)
					if rec := f.request(t, "POST", "/ui/settings/navigation/"+action, "alice", personalNavigationForm(t, forged)); rec.Code != 400 {
						t.Error("closed target forgery accepted", action, rec.Code)
					}
					if moved {
						forged = f.personalEditor(t, "alice", personalSchool)
						forged.Desired.Sections = append(forged.Desired.Sections, navigation.Section{ID: "cfg:closed-sentinel-section", Title: "Forged original ancestor"})
						if rec := f.request(t, "POST", "/ui/settings/navigation/"+action, "alice", personalNavigationForm(t, forged)); rec.Code != 400 {
							t.Error("closed ancestor forgery accepted", action, rec.Code)
						}
					}
				}
				state = f.personalEditor(t, "alice", personalSchool)
				personalSection(t, &state, "cfg:academic-years").Icon = "book-open"
				if scenario == "move-visible-out" {
					personalGroupByID(t, &state, "cfg:closed-sentinel-group").Items = nil
					academic = personalSection(t, &state, "cfg:academic-years")
					academic.Items = append(academic.Items, public)
				}
				if scenario == "hide-moved-group" {
					for i := range state.Desired.Sections {
						section := &state.Desired.Sections[i]
						if section.Title == "Visible custom section" {
							section.Groups = nil
						}
					}
				}
				if scenario == "neutral-parent-rename" {
					state.Renamed = []string{"cfg:closed-sentinel-section"}
				}
				checkPrivate(f.request(t, "POST", "/ui/settings/navigation/preview", "alice", personalNavigationForm(t, state)))
				if rec := f.request(t, "POST", "/ui/settings/navigation/save", "alice", personalNavigationForm(t, state)); rec.Code != 303 {
					t.Errorf("fresh visible save: %d", rec.Code)
				}
				if scenario == "neutral-parent-rename" {
					state = f.personalEditor(t, "alice", personalSchool)
					found := false
					for _, id := range state.Renamed {
						found = found || id == "cfg:closed-sentinel-section"
					}
					if !found || state.Origins["cfg:closed-sentinel-section"] != "personal" {
						t.Error("explicit inherited intent/provenance missing")
					}
					admin = f.editor(t, personalSchool)
					personalSection(t, &admin, "cfg:closed-sentinel-section").Title = "Next common parent"
					if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, admin)); rec.Code != 303 {
						t.Fatal("second common rename", rec.Code)
					}
					state = f.personalEditor(t, "alice", personalSchool)
					if personalSection(t, &state, "cfg:closed-sentinel-section").Title != "NEW_PARENT_SENTINEL" {
						t.Error("neutral rename did not override later common title")
					}
					personalSave(t, f, "alice", state)
				}
				if err := f.server.authRepo.AssignRole(ctx, alice.ID, grant.ID); err != nil {
					t.Fatal(err)
				}
				state = f.personalEditor(t, "alice", personalSchool)
				if personalSection(t, &state, "cfg:academic-years").Icon != "book-open" {
					t.Error("visible edit was not persisted")
				}
				if scenario != "admin-hidden-anchor" && personalGroupByID(t, &state, privateFolderID).Title != "Hidden intent folder" {
					t.Error("private intent folder was not restored")
				}
				if scenario == "hide-moved-group" {
					raw, err := json.Marshal(state.Desired)
					if err != nil || strings.Contains(string(raw), "cfg:closed-sentinel-group") || strings.Contains(string(raw), public.ID) {
						t.Error("explicit group hide lost after regrant", err)
					}
				} else if scenario == "move-visible-out" {
					items := personalSection(t, &state, "cfg:academic-years").Items
					if len(items) != 1 || items[0].ID != public.ID {
						t.Error("visible move replaced by private anchor", items)
					}
				} else if scenario == "admin-hidden-anchor" {
					// A common deletion makes the old move's anchor stale before
					// editing. Regrant must not revive the deleted target; the
					// accessible item keeps its authoritative fallback placement.
					raw, err := json.Marshal(state.Desired)
					if err != nil || strings.Contains(string(raw), private.ID) || !strings.Contains(string(raw), public.ID) {
						t.Error("common-hidden anchor resurrected or accessible item lost", err)
					}
				} else if scenario == "section-items" {
					items := personalSection(t, &state, "cfg:closed-sentinel-section").Items
					if len(items) != 1 || items[0].ID != public.ID {
						t.Error("direct section placement lost", items)
					}
				} else if publicFolderID != "" {
					items := personalGroupByID(t, &state, publicFolderID).Items
					if len(items) != 1 || items[0].ID != public.ID {
						t.Error("personal folder placement lost", items)
					}
				} else {
					items := personalGroupByID(t, &state, "cfg:closed-sentinel-group").Items
					if len(items) != 2 || items[0].ID != private.ID || items[1].ID != public.ID {
						t.Error("inherited group placement/order lost", items)
					}
				}
			})
		})
	}
}

func TestPersonalNavigation_PrivatePlacementWithAccessibleAnchor(t *testing.T) {
	for _, scenario := range []struct {
		name string
		want []string
	}{
		{"unchanged", []string{"cfg:academic-years-list", "cfg:periods-list", "cfg:bells"}},
		{"move-out", []string{"cfg:periods-list", "cfg:bells"}},
		{"reorder", []string{"cfg:bells", "cfg:academic-years-list", "cfg:periods-list"}},
		{"hide-original-parent", []string{"cfg:academic-years-list", "cfg:periods-list", "cfg:bells"}},
		{"admin-hidden", []string{"cfg:academic-years-list", "cfg:bells"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				f := schoolNavigationFixture(t, db, "testdata/navigation-school", true)
				state := f.personalEditor(t, "alice", personalSchool)
				section := personalSection(t, &state, "cfg:academic-years")
				anchor, private, sibling := section.Items[0], section.Groups[0].Items[0], section.Groups[1].Items[0]
				section.Items = section.Items[1:]
				section.Groups[0].Items = nil
				section.Groups[1].Items = section.Groups[1].Items[1:]
				parent := section
				if scenario.name == "hide-original-parent" {
					parent = personalSection(t, &state, "cfg:journals")
				}
				items := []navigation.Item{anchor, private, sibling}
				if scenario.name == "admin-hidden" {
					items = []navigation.Item{anchor, sibling, private}
				}
				parent.Groups = append(parent.Groups, navigation.Group{ID: "new:private-mix", Title: "Private mix", Items: items})
				personalSave(t, f, "alice", state)
				state = f.personalEditor(t, "alice", personalSchool)
				var folderID string
				for _, section := range state.Desired.Sections {
					for _, group := range section.Groups {
						if group.Title == "Private mix" {
							folderID = group.ID
						}
					}
				}
				if !strings.HasPrefix(folderID, "usr:") || len(personalGroupByID(t, &state, folderID).Items) != 3 {
					t.Fatal("initial placement was not saved", folderID)
				}
				regrant := personalRevokePeriods(t, f)
				if scenario.name == "admin-hidden" {
					admin := f.editor(t, personalSchool)
					personalSection(t, &admin, "cfg:academic-years").Groups[0].Items = nil
					if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, admin)); rec.Code != 303 {
						t.Fatal("common hide", rec.Code)
					}
				}
				state = f.personalEditor(t, "alice", personalSchool)
				group := personalGroupByID(t, &state, folderID)
				if len(group.Items) != 2 || group.Items[0].ID != anchor.ID || group.Items[1].ID != sibling.ID {
					t.Fatal("permission projection", group.Items)
				}
				switch scenario.name {
				case "move-out":
					group.Items = group.Items[1:]
					section = personalSection(t, &state, "cfg:academic-years")
					section.Items = append(section.Items, anchor)
				case "reorder":
					group.Items[0], group.Items[1] = group.Items[1], group.Items[0]
				case "hide-original-parent":
					for i, section := range state.Desired.Sections {
						if section.ID == "cfg:academic-years" {
							state.Desired.Sections = append(state.Desired.Sections[:i], state.Desired.Sections[i+1:]...)
							break
						}
					}
				}
				personalSection(t, &state, "cfg:journals").Icon = "book-open"
				personalSave(t, f, "alice", state)
				state = f.personalEditor(t, "alice", personalSchool)
				checkPrivate := func(rec *httptest.ResponseRecorder) {
					t.Helper()
					if rec.Code != 200 {
						t.Fatal("private response status", rec.Code)
					}
					for _, denied := range []string{"cfg:periods-list", "ПериодыОбучения", "Периоды обучения", "ClosedSentinel", "SECRET_", "closed-sentinel", url.PathEscape(strings.ToLower("ПериодыОбучения"))} {
						if strings.Contains(rec.Body.String(), denied) {
							t.Fatal("closed metadata in public response", denied)
						}
					}
				}
				checkPrivate(f.request(t, "GET", "/ui/settings/navigation?subsystem="+url.QueryEscape(personalSchool), "alice", nil))
				checkPrivate(f.request(t, "POST", "/ui/settings/navigation/preview", "alice", personalNavigationForm(t, state)))
				checkPrivate(f.request(t, "GET", "/ui?subsystem="+url.QueryEscape(personalSchool), "alice", nil))
				if rec := f.request(t, "GET", "/ui/catalog/"+url.PathEscape(strings.ToLower("ПериодыОбучения")), "alice", nil); rec.Code != 403 {
					t.Fatal("revoked direct URL", rec.Code)
				}
				admin := f.editor(t, personalSchool)
				personalSection(t, &admin, "cfg:academic-years").Title = "Changed common"
				if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, admin)); rec.Code != 303 {
					t.Fatal("common rename", rec.Code)
				}
				regrant()
				state = f.personalEditor(t, "alice", personalSchool)
				group = personalGroupByID(t, &state, folderID)
				if len(group.Items) != len(scenario.want) {
					t.Fatalf("private placement lost: got %v, want %v", group.Items, scenario.want)
				}
				for i, id := range scenario.want {
					if group.Items[i].ID != id {
						t.Fatalf("private order: got %v, want %v", group.Items, scenario.want)
					}
				}
				if scenario.name == "move-out" {
					items := personalSection(t, &state, "cfg:academic-years").Items
					if len(items) != 1 || items[0].ID != anchor.ID {
						t.Fatal("visible move lost", items)
					}
				}
				if scenario.name == "admin-hidden" {
					raw, err := json.Marshal(state.Desired)
					if err != nil || strings.Contains(string(raw), "cfg:periods-list") {
						t.Fatal("common-hidden target resurrected", err)
					}
				}
			})
		})
	}
}

func TestPersonalNavigation_AuthCSRFMalformedAndCorruptMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := schoolNavigationFixture(t, db, "testdata/navigation-school", true)
		state := f.personalEditor(t, "alice", personalSchool)
		for _, action := range []string{"", "/preview", "/save", "/reset"} {
			method := "POST"
			if action == "" {
				method = "GET"
			}
			req := httptest.NewRequest(method, "/ui/settings/navigation"+action, strings.NewReader(personalNavigationForm(t, state).Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			f.plain.ServeHTTP(rec, req)
			if rec.Code != 403 {
				t.Fatal("anonymous handler", action, rec.Code)
			}
			req = httptest.NewRequest(method, "/ui/settings/navigation"+action, strings.NewReader(personalNavigationForm(t, state).Encode()))
			req.AddCookie(f.cookie["alice"])
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", "https://evil.example")
			rec = httptest.NewRecorder()
			f.http.ServeHTTP(rec, req)
			if method == "POST" && rec.Code != 403 {
				t.Fatal("CSRF", action, rec.Code)
			}
		}
		for _, mutate := range []func(url.Values){
			func(form url.Values) { form.Set("renamed", `["cfg:closed-sentinel-item"]`) },
			func(form url.Values) { form.Set("renamed", `["cfg:academic-years","cfg:academic-years"]`) },
			func(form url.Values) { form.Set("renamed", `[] {}`) },
			func(form url.Values) { form.Set("desired", form.Get("desired")+` {}`) },
			func(form url.Values) { form["revision"] = []string{"", ""} },
			func(form url.Values) { form.Del("base_revision") },
		} {
			form := personalNavigationForm(t, state)
			mutate(form)
			if rec := f.request(t, "POST", "/ui/settings/navigation/save", "alice", form); rec.Code != 400 {
				t.Fatal("malformed", rec.Code)
			}
		}
		section := personalSection(t, &state, "cfg:academic-years")
		section.Title, section.Titles = `</script><img src=x onerror=alert(1)>`, nil
		state.Renamed = []string{section.ID}
		personalSave(t, f, "alice", state)
		page := f.request(t, "GET", "/ui/settings/navigation?subsystem="+url.QueryEscape(personalSchool), "alice", nil)
		if strings.Contains(page.Body.String(), `</script><img`) || strings.Contains(page.Body.String(), `<img src=x`) {
			t.Fatal("unescaped personal HTML")
		}
		state = decodeNavigationBootstrap(t, page)
		if personalSection(t, &state, "cfg:academic-years").Title != section.Title {
			t.Fatal("literal title lost")
		}
		key := storage.NavigationUserPrefix + "5:alice." + strconv.Itoa(len("subsystem:"+personalSchool)) + ":subsystem:" + personalSchool
		if err := db.SaveSetting(context.Background(), key, "{ broken SECRET_CLOSED_RULE }"); err != nil {
			t.Fatal(err)
		}
		page = f.request(t, "GET", "/ui/settings/navigation?subsystem="+url.QueryEscape(personalSchool), "alice", nil)
		if page.Code != 200 || strings.Contains(page.Body.String(), "SECRET_") || !strings.Contains(page.Body.String(), "Повреждённая личная настройка") {
			t.Fatal("unsafe corrupt fallback")
		}
		state = decodeNavigationBootstrap(t, page)
		if state.Revision == "" || personalSection(t, &state, "cfg:academic-years").Title != "Учебные годы" {
			t.Fatal("corrupt user did not fall back")
		}
		if rec := f.request(t, "POST", "/ui/settings/navigation/reset", "alice", url.Values{"subsystem": {personalSchool}, "revision": {state.Revision}}); rec.Code != 303 {
			t.Fatal("corrupt reset", rec.Code)
		}
	})
}

func TestPersonalNavigation_RestartAndBackupRestore(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "restart.sqlite")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	f := schoolNavigationFixture(t, db, "testdata/navigation-school", true)
	admin := f.editor(t, personalSchool)
	personalSection(t, &admin, "cfg:academic-years").Title = "Restart common"
	personalSection(t, &admin, "cfg:academic-years").Titles = nil
	if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, admin)); rec.Code != 303 {
		t.Fatal(rec.Code)
	}
	for _, login := range []string{"alice", "bob"} {
		state := f.personalEditor(t, login, personalSchool)
		personalSection(t, &state, "cfg:academic-years").Title = login + " persisted"
		state.Renamed = []string{"cfg:academic-years"}
		personalSave(t, f, login, state)
	}
	f.server.Close()
	db.Close()
	db, err = storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	f = schoolNavigationFixture(t, db, "testdata/navigation-school", false)
	assertRestored := func(f navigationHTTPFixture) {
		t.Helper()
		if f.editor(t, personalSchool).Desired.Sections[0].Title != "Restart common" {
			t.Fatal("common lost")
		}
		for _, login := range []string{"alice", "bob"} {
			state := f.personalEditor(t, login, personalSchool)
			if personalSection(t, &state, "cfg:academic-years").Title != login+" persisted" {
				t.Fatal("personal lost", login)
			}
			page := f.request(t, "GET", "/ui/?subsystem="+url.QueryEscape(personalSchool), login, nil)
			if page.Code != 200 || !strings.Contains(page.Body.String(), login+" persisted") || strings.Contains(page.Body.String(), "SECRET_") {
				t.Fatal("restored runtime or rights lost")
			}
		}
	}
	assertRestored(f)
	var archive bytes.Buffer
	if err := backup.ExportUniversal(ctx, db, "file", "testdata/navigation-school", "", "School", &archive); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []backup.ExchangeRestoreMode{backup.ExchangeRestoreClone, backup.ExchangeRestoreDisasterRecovery} {
		t.Run(string(mode), func(t *testing.T) {
			target, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "restored.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(target.Close)
			if err := target.EnsureServiceSchema(ctx); err != nil {
				t.Fatal(err)
			}
			configDir := filepath.Join(t.TempDir(), "School")
			if _, err := backup.ImportUniversalWithOptions(ctx, target, "file", configDir, "", bytes.NewReader(archive.Bytes()), int64(archive.Len()), backup.ImportOptions{ExchangeMode: mode}); err != nil {
				t.Fatal(err)
			}
			// Fresh sessions against restored accounts/roles, not exported cookies.
			assertRestored(schoolNavigationFixture(t, target, configDir, false))
		})
	}
}

func TestPersonalNavigation_ConfigurationEvolution(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("testdata/navigation-school")); err != nil {
		t.Fatal(err)
	}
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "evolution.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	f := schoolNavigationFixture(t, db, dir, true)
	state := f.personalEditor(t, "alice", personalSchool)
	section := personalSection(t, &state, "cfg:academic-years")
	period := section.Groups[0].Items[0]
	section.Groups[0].Items = nil
	section.Groups = append(section.Groups, navigation.Group{ID: "new:mixed", Title: "My mixed folder", Items: []navigation.Item{period, section.Groups[2].Items[0]}})
	section.Groups[2].Items = nil
	personalSave(t, f, "alice", state)
	f.server.Close()
	// Reload actual YAML: the explicit ID remains stable despite a title change;
	// one old target disappears and a new unplaced contents member is inherited.
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	path := filepath.Join("subsystems", personalSchool+".yaml")
	raw, err := root.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(strings.ReplaceAll(string(raw), "\r\n", "\n"), "title: Учебные годы", "title: Changed YAML title", 1)
	updated = strings.Replace(updated, "    - Классы", "    - Классы\n    - NewCourse", 1)
	updated = strings.ReplaceAll(updated, "    - ПриказОНачалеУчебногоГода\n", "")
	updated = strings.ReplaceAll(updated, "        - id: orders\n          title: Приказы по учебному году\n          items:\n            - id: opening-order\n              target: document:ПриказОНачалеУчебногоГода\n", "")
	if err := root.WriteFile(path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "catalogs", "NewCourse.yaml"), []byte("name: NewCourse\ntitle: New course\nfields:\n  - name: Name\n    type: string\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "documents", "ПриказОНачалеУчебногоГода.yaml")); err != nil {
		t.Fatal(err)
	}
	teacher, err := auth.LoadRoleFile(filepath.Join(dir, "roles", "Teacher.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	teacher.Permissions.Catalogs["NewCourse"] = []string{"read"}
	if err := f.server.authRepo.SyncRoles(ctx, []*auth.Role{teacher}); err != nil {
		t.Fatal(err)
	}
	f = schoolNavigationFixture(t, db, dir, false)
	state = f.personalEditor(t, "alice", personalSchool)
	section = personalSection(t, &state, "cfg:academic-years")
	if section.Title != "Changed YAML title" {
		t.Fatal("title change reset inherited identity")
	}
	if _, present := navigationEditorNodes(state.Desired)["cfg:opening-order"]; present {
		t.Fatal("removed target restored")
	}
	found := false
	for _, section := range state.Desired.Sections {
		for _, item := range section.Items {
			if item.Object.Target.Name == "NewCourse" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("new unplaced contents not inherited")
	}
	personalSave(t, f, "alice", state)
	setting, err := db.GetNavigationSettings(ctx, storage.NavigationSettingsScope{Layer: navigation.UserLayer, Login: "alice", Context: "subsystem:" + personalSchool})
	if err != nil || strings.Contains(setting.Raw, "opening-order") {
		t.Fatal("explicit resave did not drop stale op", err)
	}
}
