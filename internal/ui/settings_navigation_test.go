package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/navigation"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/websec"
	"golang.org/x/net/html"
)

type navigationHTTPFixture struct {
	server *Server
	http   http.Handler
	plain  http.Handler
	cookie map[string]*http.Cookie
}

func newNavigationHTTPFixture(t *testing.T) navigationHTTPFixture {
	t.Helper()
	s := semanticSchoolServer(t, "School")
	ctx := context.Background()
	for _, ensure := range []func(context.Context) error{s.authRepo.EnsureSchema, s.authRepo.EnsureRolesSchema, s.store.EnsureAuditSchema} {
		if err := ensure(ctx); err != nil {
			t.Fatal(err)
		}
	}
	role := semanticTeacher().Roles[0]
	if err := s.authRepo.SyncRoles(ctx, []*auth.Role{role}); err != nil {
		t.Fatal(err)
	}
	cookies := map[string]*http.Cookie{}
	for _, login := range []string{"admin", "alice", "bob"} {
		user, err := s.authRepo.Create(ctx, login, "secret123", login, login == "admin")
		if err != nil {
			t.Fatal(err)
		}
		if login != "admin" {
			if err := s.authRepo.AssignRole(ctx, user.ID, role.ID); err != nil {
				t.Fatal(err)
			}
		}
		token, err := s.authRepo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
		if err != nil {
			t.Fatal(err)
		}
		cookies[login] = &http.Cookie{Name: "onebase_session", Value: token}
	}
	router := chi.NewRouter()
	router.Use(websec.CSRFProtect)
	s.MountStatic(router)
	router.Group(func(group chi.Router) { group.Use(s.authRepo.Middleware); s.Mount(group) })
	plain := chi.NewRouter()
	plain.Use(websec.CSRFProtect)
	s.Mount(plain)
	return navigationHTTPFixture{s, router, plain, cookies}
}

func (f navigationHTTPFixture) request(t *testing.T, method, path, login string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie := f.cookie[login]; cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	f.http.ServeHTTP(rec, req)
	return rec
}

type navigationBootstrap struct {
	Base      navigation.Tree            `json:"base"`
	Desired   navigation.Tree            `json:"desired"`
	Revision  string                     `json:"revision"`
	Subsystem string                     `json:"subsystem"`
	Preview   []NavigationPreviewSection `json:"preview"`
}

func (f navigationHTTPFixture) editor(t *testing.T, sub string) navigationBootstrap {
	t.Helper()
	return decodeNavigationBootstrap(t, f.request(t, "GET", "/ui/admin/navigation?"+url.Values{"subsystem": {sub}}.Encode(), "admin", nil))
}

func decodeNavigationBootstrap(t *testing.T, rec *httptest.ResponseRecorder) navigationBootstrap {
	t.Helper()
	if rec.Code != http.StatusOK && rec.Code != http.StatusConflict {
		t.Fatalf("editor: %d %s", rec.Code, rec.Body.String())
	}
	doc, err := html.Parse(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	node := semanticFind(doc, "id", "navigation-editor-data")
	if node == nil || node.FirstChild == nil {
		t.Fatalf("missing bootstrap: %s", rec.Body.String())
	}
	var result navigationBootstrap
	if err := json.Unmarshal([]byte(node.FirstChild.Data), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func navigationForm(t *testing.T, state navigationBootstrap) url.Values {
	t.Helper()
	raw, err := json.Marshal(state.Desired)
	if err != nil {
		t.Fatal(err)
	}
	return url.Values{"subsystem": {state.Subsystem}, "revision": {state.Revision}, "desired": {string(raw)}}
}

func TestNavigationSettings_AdminSavePreviewRuntimeResetAndAudit(t *testing.T) {
	f := newNavigationHTTPFixture(t)
	state := f.editor(t, "School")
	state.Desired.Sections[0].Title = "Общие учебные годы"
	state.Desired.Sections[0].Titles = nil
	item := state.Desired.Sections[0].Groups[0].Items[1]
	state.Desired.Sections[0].Groups[0].Items = append(state.Desired.Sections[0].Groups[0].Items[:1], state.Desired.Sections[0].Groups[0].Items[2:]...)
	state.Desired.Sections[0].Groups = append(state.Desired.Sections[0].Groups, navigation.Group{ID: "new:school-orders", Title: "Общие приказы", Icon: "calendar-days", Items: []navigation.Item{item}})
	state.Desired.Sections[0].Groups[1].Items = nil // restricted group stays empty in DTO
	form := navigationForm(t, state)
	preview := f.request(t, "POST", "/ui/admin/navigation/preview", "admin", form)
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), "Общие приказы") {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	if f.editor(t, "School").Revision != "" {
		t.Fatal("preview wrote a setting")
	}
	response := f.request(t, "POST", "/ui/admin/navigation/save", "admin", form)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("save: %d %s", response.Code, response.Body.String())
	}
	saved := f.editor(t, "School")
	custom := saved.Desired.Sections[0].Groups[2]
	if !strings.HasPrefix(custom.ID, "adm:") || custom.ID == "new:school-orders" || custom.Items[0].ID != item.ID {
		t.Fatal("server identity allocation or mixed-kind move lost")
	}
	for _, login := range []string{"alice", "bob"} {
		page := f.request(t, "GET", "/ui/?subsystem=School", login, nil)
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Общие учебные годы") || !strings.Contains(page.Body.String(), "Общие приказы") {
			t.Fatalf("%s common menu missing: %d", login, page.Code)
		}
		doc, err := html.Parse(strings.NewReader(page.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		folder := semanticFind(doc, "data-nav-id", custom.ID)
		if folder == nil || !strings.Contains(strings.Join(semanticLinks(folder), "|"), "/ui/document/Orders") || semanticFind(doc, "data-nav-id", "cfg:restricted-section") != nil {
			t.Fatal("move or current RBAC lost")
		}
	}
	// A second save reuses the server-issued container identity.
	saved.Desired.Sections[0].Groups[2].Icon = "book-open"
	if got := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, saved)); got.Code != http.StatusSeeOther {
		t.Fatalf("resave issued ID: %d %s", got.Code, got.Body.String())
	}
	saved = f.editor(t, "School")
	if got := f.request(t, "POST", "/ui/admin/navigation/reset", "admin", url.Values{"subsystem": {"School"}, "revision": {saved.Revision}}); got.Code != http.StatusSeeOther {
		t.Fatalf("reset: %d", got.Code)
	}
	after := f.editor(t, "School")
	if after.Revision != "" || after.Desired.Sections[0].Title != state.Base.Sections[0].Title {
		t.Fatal("reset did not return to configuration")
	}
	entries, err := f.server.store.AuditSearch(context.Background(), storage.AuditFilter{EntityName: "subsystem:School"}, 100, 0)
	if err != nil || len(entries) != 3 {
		t.Fatalf("audit count=%d: %v", len(entries), err)
	}
	for _, entry := range entries {
		raw, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		if entry.UserLogin != "admin" || entry.UserID == "" || entry.EntityKind != "navigation" || !strings.Contains(string(raw), "revision") || strings.Contains(string(raw), "Общие") || strings.Contains(string(raw), "base_hash") || strings.Contains(string(raw), "cfg:") {
			t.Fatalf("unsafe or incomplete audit: %s", raw)
		}
	}
}

func TestNavigationSettings_ForbiddenAndCSRFDoNotWrite(t *testing.T) {
	f := newNavigationHTTPFixture(t)
	state := f.editor(t, "School")
	state.Desired.Sections[0].Title = "Forbidden rename"
	for _, path := range []string{"/ui/admin/navigation", "/ui/admin/navigation/preview", "/ui/admin/navigation/save", "/ui/admin/navigation/reset"} {
		method := "POST"
		if path == "/ui/admin/navigation" {
			method = "GET"
		}
		if rec := f.request(t, method, path, "alice", navigationForm(t, state)); rec.Code != http.StatusForbidden {
			t.Fatalf("non-admin %s: %d", path, rec.Code)
		}
		req := httptest.NewRequest(method, path, strings.NewReader(navigationForm(t, state).Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		f.plain.ServeHTTP(rec, req) // public handler boundary without a trusted user
		if rec.Code != http.StatusForbidden {
			t.Fatalf("anonymous handler %s: %d", path, rec.Code)
		}
	}
	if rec := f.request(t, "GET", "/ui/admin/navigation", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("deployment auth boundary: %d", rec.Code)
	}
	for _, path := range []string{"preview", "save", "reset"} {
		req := httptest.NewRequest("POST", "/ui/admin/navigation/"+path, strings.NewReader(navigationForm(t, state).Encode()))
		req.AddCookie(f.cookie["admin"])
		req.Header.Set("Origin", "https://evil.example")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		f.http.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("CSRF %s: %d", path, rec.Code)
		}
	}
	if f.editor(t, "School").Revision != "" {
		t.Fatal("forbidden request wrote settings")
	}
}

func TestNavigationSettings_StaleSaveAndResetCannotOverwrite(t *testing.T) {
	f := newNavigationHTTPFixture(t)
	first, stale := f.editor(t, "School"), f.editor(t, "School")
	first.Desired.Sections[0].Title, first.Desired.Sections[0].Titles = "First tab", nil
	if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, first)); rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Body.String())
	}
	winner := f.editor(t, "School")
	stale.Desired.Sections[0].Title, stale.Desired.Sections[0].Titles = "Stale tab", nil
	for _, operation := range []string{"save", "reset"} {
		rec := f.request(t, "POST", "/ui/admin/navigation/"+operation, "admin", navigationForm(t, stale))
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "другой вкладке") || decodeNavigationBootstrap(t, rec).Revision != winner.Revision {
			t.Fatalf("stale %s: %d %s", operation, rec.Code, rec.Body.String())
		}
	}
	if f.editor(t, "School").Revision != winner.Revision {
		t.Fatal("winner overwritten")
	}
	req := httptest.NewRequest("POST", "/ui/admin/navigation/save", strings.NewReader(navigationForm(t, stale).Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.AddCookie(f.cookie["admin"])
	conflict := httptest.NewRecorder()
	f.http.ServeHTTP(conflict, req)
	var latest struct {
		Revision string                     `json:"revision"`
		Preview  []NavigationPreviewSection `json:"preview"`
	}
	if err := json.Unmarshal(conflict.Body.Bytes(), &latest); err != nil || conflict.Code != http.StatusConflict || latest.Revision != winner.Revision || latest.Preview[0].Title != "First tab" {
		t.Fatalf("JSON conflict snapshot: %d %s %v", conflict.Code, conflict.Body.String(), err)
	}
	winner.Desired.Sections[0].Groups = append(winner.Desired.Sections[0].Groups, navigation.Group{ID: "new:stale-custom", Title: "Temporary group"})
	if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, winner)); rec.Code != http.StatusSeeOther {
		t.Fatal("custom setup failed")
	}
	withCustom := f.editor(t, "School")
	if rec := f.request(t, "POST", "/ui/admin/navigation/reset", "admin", url.Values{"subsystem": {"School"}, "revision": {withCustom.Revision}}); rec.Code != http.StatusSeeOther {
		t.Fatal("concurrent reset failed")
	}
	if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, withCustom)); rec.Code != http.StatusConflict {
		t.Fatalf("stale custom identity must conflict before validation: %d", rec.Code)
	}
}

func TestNavigationSettings_InvalidInputDoesNotWrite(t *testing.T) {
	f := newNavigationHTTPFixture(t)
	state := f.editor(t, "School")
	valid := navigationForm(t, state)
	cases := map[string]url.Values{}
	for name, raw := range map[string]string{"malformed": "{", "null": "null", "unknown": `{"version":1,"context":"subsystem:School","sections":[],"url":"https://evil.example"}`, "trailing": valid.Get("desired") + "{}", "oversize": strings.Repeat(" ", maxNavigationFormBytes+1)} {
		cases[name] = url.Values{"subsystem": {"School"}, "revision": {""}, "desired": {raw}}
	}
	cases["scope-injection"] = url.Values{"subsystem": {"School"}, "revision": {""}, "desired": {valid.Get("desired")}, "login": {"bob"}}
	cases["duplicate-field"] = url.Values{"subsystem": {"School", "global"}, "revision": {""}, "desired": {valid.Get("desired")}}
	cases["missing-revision"] = url.Values{"subsystem": {"School"}, "desired": {valid.Get("desired")}}
	for _, mutation := range []string{"target", "duplicate", "cycle", "foreign-custom", "membership", "context"} {
		copy := f.editor(t, "School")
		switch mutation {
		case "target":
			copy.Desired.Sections[0].Groups[0].Items[0].Target = "https://evil.example"
		case "duplicate":
			copy.Desired.Sections = append(copy.Desired.Sections, copy.Desired.Sections[0])
		case "cycle":
			copy.Desired.Sections[0].Groups[0].ID = copy.Desired.Sections[0].ID
		case "foreign-custom":
			copy.Desired.Sections[0].Groups = append(copy.Desired.Sections[0].Groups, navigation.Group{ID: "adm:11111111-1111-4111-8111-111111111111", Title: "Forged"})
		case "membership":
			copy.Desired.Sections[0].Items = append(copy.Desired.Sections[0].Items, navigation.Item{ID: "cfg:new", Target: "catalog:Outside"})
		case "context":
			copy.Desired.Context = "global"
		}
		cases[mutation] = navigationForm(t, copy)
	}
	for name, form := range cases {
		t.Run(name, func(t *testing.T) {
			if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", form); rec.Code != http.StatusBadRequest {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			if f.editor(t, "School").Revision != "" {
				t.Fatal("invalid input wrote settings")
			}
		})
	}
}

func TestNavigationSettings_CorruptLayerWarningAndCASReset(t *testing.T) {
	f := newNavigationHTTPFixture(t)
	if err := f.server.store.EnsureSettingsSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.store.Exec(context.Background(), "INSERT INTO _settings (key,value) VALUES (?,?)", "ui.navigation.admin.16:subsystem:School", "broken secret <script>"); err != nil {
		t.Fatal(err)
	}
	rec := f.request(t, "GET", "/ui/admin/navigation?subsystem=School", "admin", nil)
	state := decodeNavigationBootstrap(t, rec)
	if state.Revision == "" || state.Desired.Sections[0].Title != state.Base.Sections[0].Title || !strings.Contains(rec.Body.String(), "Повреждённая общая настройка") || strings.Contains(rec.Body.String(), "broken secret") {
		t.Fatal("unsafe corrupt-layer preview")
	}
	if rec := f.request(t, "POST", "/ui/admin/navigation/reset", "admin", url.Values{"subsystem": {"School"}, "revision": {state.Revision}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("corrupt reset: %d", rec.Code)
	}
	if f.editor(t, "School").Revision != "" {
		t.Fatal("corrupt layer survived reset")
	}
}

func TestNavigationSettings_ExplicitSameTitleRenameAndFixedChrome(t *testing.T) {
	f := newNavigationHTTPFixture(t)
	state := f.editor(t, "School")
	state.Desired.Sections[0].Titles = nil
	if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, state)); rec.Code != http.StatusSeeOther {
		t.Fatalf("same-title explicit rename: %d %s", rec.Code, rec.Body.String())
	}
	if f.editor(t, "School").Desired.Sections[0].Titles != nil {
		t.Fatal("explicit rename retained localized titles")
	}
	saved := f.editor(t, "School")
	saved.Desired.Sections = nil
	if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, saved)); rec.Code != http.StatusSeeOther {
		t.Fatalf("hide everything: %d", rec.Code)
	}
	admin := f.request(t, "GET", "/ui/?subsystem=School", "admin", nil)
	alice := f.request(t, "GET", "/ui/?subsystem=School", "alice", nil)
	if !strings.Contains(admin.Body.String(), "/ui/admin/navigation") || strings.Contains(alice.Body.String(), "/ui/admin/navigation") {
		t.Fatal("fixed admin chrome visibility lost")
	}
	if asset := f.request(t, "GET", "/static/settings-navigation.js", "", nil); asset.Code != http.StatusOK || !strings.Contains(asset.Body.String(), "navigation-editor-data") || asset.Header().Get("ETag") == "" {
		t.Fatal("editor production asset missing")
	}
}

func TestNavigationSettingsBrowserBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required for navigation editor behavior")
	}
	cmd := exec.Command(node, "--test", "static/settings_navigation_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("navigation editor behavior: %v\n%s", err, output)
	}
}

func TestNavigationSettings_GlobalAndSubsystemIsolationXSSAndFreshConfiguration(t *testing.T) {
	f := newNavigationHTTPFixture(t)
	global, school := f.editor(t, ""), f.editor(t, "School")
	global.Desired.Sections[0].Title = "Global common title"
	global.Desired.Sections[0].Titles = nil
	malicious := `<script>alert("navigation-title")</script>`
	school.Desired.Sections[0].Title, school.Desired.Sections[0].Titles = malicious, nil
	for _, state := range []navigationBootstrap{global, school} {
		if rec := f.request(t, "POST", "/ui/admin/navigation/save?subsystem=IgnoredQuery", "admin", navigationForm(t, state)); rec.Code != http.StatusSeeOther {
			t.Fatalf("scoped save: %d %s", rec.Code, rec.Body.String())
		}
	}
	page := f.request(t, "GET", "/ui/?subsystem=School", "alice", nil)
	if page.Code != http.StatusOK || strings.Contains(page.Body.String(), malicious) {
		t.Fatal("title escaped incorrectly")
	}
	doc, err := html.Parse(strings.NewReader(page.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	if semanticHeading(semanticFind(doc, "data-nav-id", "cfg:education")) != malicious {
		t.Fatal("title was not preserved as text")
	}
	sub := f.server.reg.GetSubsystem("School")
	sub.Menu.Sections[0].Items = append(sub.Menu.Sections[0].Items, metadata.MenuItem{ID: "new-years", Target: "catalog:Years"})
	updated := f.request(t, "GET", "/ui/?subsystem=School", "alice", nil)
	doc, err = html.Parse(strings.NewReader(updated.Body.String()))
	if err != nil || semanticFind(doc, "data-nav-id", "cfg:new-years") == nil {
		t.Fatal("new configuration node frozen by old delta")
	}
	current := f.editor(t, "School")
	if rec := f.request(t, "POST", "/ui/admin/navigation/reset", "admin", url.Values{"subsystem": {"School"}, "revision": {current.Revision}}); rec.Code != http.StatusSeeOther {
		t.Fatal("scoped reset failed")
	}
	if after := f.editor(t, ""); after.Revision == "" || after.Desired.Sections[0].Title != global.Desired.Sections[0].Title {
		t.Fatal("subsystem reset changed global layer")
	}
	// Current permissions are read again by the real session middleware.
	role := semanticTeacher().Roles[0]
	role.Permissions.Catalogs = map[string][]string{}
	if err := f.server.authRepo.SyncRoles(context.Background(), []*auth.Role{role}); err != nil {
		t.Fatal(err)
	}
	restricted := f.request(t, "GET", "/ui/?subsystem=School", "alice", nil)
	if strings.Contains(restricted.Body.String(), "href=\"/ui/catalog/Classes") || strings.Contains(restricted.Body.String(), "href=\"/ui/catalog/Years") {
		t.Fatal("saved layout bypassed changed permissions")
	}
}

// Exercise the mounted editor and runtime with sessions, CSRF and persisted
// SQLite settings. Returning to the configuration text is still an explicit
// rename, not a request to inherit the legacy heading's UI translation.
func TestNavigationSettings_LegacyExplicitTitleBackToConfiguration(t *testing.T) {
	f := newNavigationHTTPFixture(t)
	f.server.reg.GetSubsystem("School").Menu = nil
	bundle, err := i18n.Load(i18n.EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	f.server.cfg.Bundle, f.server.cfg.Lang = bundle, "en"
	const id = "cfg:legacy-catalog"
	heading := func(login, want string) {
		t.Helper()
		rec := f.request(t, "GET", "/ui/?subsystem=School", login, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("runtime: %d %s", rec.Code, rec.Body.String())
		}
		doc, err := html.Parse(strings.NewReader(rec.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		node := semanticFind(doc, "data-nav-id", id)
		if node == nil {
			t.Fatal("legacy catalog section missing")
		}
		if got := semanticHeading(node); got != want {
			t.Fatalf("%s heading = %q, want %q", login, got, want)
		}
	}
	heading("admin", "Catalogs")
	original := ""
	for _, section := range f.editor(t, "School").Base.Sections {
		if section.ID == id {
			original = section.Title
		}
	}
	if original != "Справочники" {
		t.Fatalf("unexpected configuration title: %q", original)
	}
	for _, title := range []string{"Temporary explicit title", original} {
		state := f.editor(t, "School")
		for i := range state.Desired.Sections {
			if state.Desired.Sections[i].ID == id {
				state.Desired.Sections[i].Title = title
				state.Desired.Sections[i].Titles = nil
			}
		}
		rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, state))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("save %q: %d %s", title, rec.Code, rec.Body.String())
		}
		heading("admin", title)
		heading("alice", title)
	}
	for _, lang := range bundle.Available() {
		f.server.cfg.Lang = lang.Code
		heading("admin", original)
		heading("alice", original)
		saved := f.editor(t, "School")
		for _, section := range saved.Preview {
			if section.ID == id && section.Title != original {
				t.Fatalf("%s preview heading = %q", lang.Code, section.Title)
			}
		}
	}
	// An unrelated edit and save must retain the explicit title's provenance.
	f.server.cfg.Lang = "en"
	saved := f.editor(t, "School")
	saved.Desired.Sections[0].Icon = "book-open"
	if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, saved)); rec.Code != http.StatusSeeOther {
		t.Fatalf("resave: %d %s", rec.Code, rec.Body.String())
	}
	heading("alice", original)
	saved = f.editor(t, "School")
	if rec := f.request(t, "POST", "/ui/admin/navigation/reset", "admin", url.Values{"subsystem": {"School"}, "revision": {saved.Revision}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body.String())
	}
	heading("alice", "Catalogs")
	// Editing the title directly to the same text also counts as a rename; the
	// browser sends title_explicit even without an intermediate different title.
	saved = f.editor(t, "School")
	for i := range saved.Desired.Sections {
		if saved.Desired.Sections[i].ID == id {
			saved.Desired.Sections[i].TitleExplicit = true
		}
	}
	if rec := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, saved)); rec.Code != http.StatusSeeOther {
		t.Fatalf("direct same-title save: %d %s", rec.Code, rec.Body.String())
	}
	heading("alice", original)
}
