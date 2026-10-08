package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/page"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"golang.org/x/net/html"
)

func semanticSchoolServer(t *testing.T, name string) *Server {
	t.Helper()
	s := newNavSortServer(t)
	s.authRepo = auth.NewRepo(s.store)
	s.reg.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{
			{Name: "Years", Kind: metadata.KindCatalog, Title: "Учебные годы", Titles: map[string]string{"en": "Academic years"}},
			{Name: "Classes", Kind: metadata.KindCatalog, Title: "Default classes", Titles: map[string]string{"ru": "Классы"}},
			{Name: "Orders", Kind: metadata.KindDocument, Title: "Приказы", Titles: map[string]string{"en": "Orders"}},
			{Name: "Private", Kind: metadata.KindCatalog, Title: "Private object"},
		},
		Registers: []*metadata.Register{{Name: "Attendance", Title: "Посещаемость", Titles: map[string]string{"en": "Attendance"}}},
		InfoRegs:  []*metadata.InfoRegister{{Name: "Bells", Title: "Звонки", Titles: map[string]string{"en": "Bells"}, Periodic: true}},
		Constants: []*metadata.Constant{{Name: "School"}},
	})
	s.reg.LoadProcessors([]*processor.Processor{{Name: "ClassJournal", Title: "Классный журнал", Titles: map[string]string{"en": "Class journal"}}, {Name: "External", Title: "Untrusted", External: true}})
	s.reg.LoadJournals([]*metadata.Journal{{Name: "Documents", Title: "Документы школы", Documents: []string{"Orders"}}})
	s.reg.LoadPages([]*page.Page{{Name: "TeacherPage", Title: "Teacher page", Roles: []string{"Teacher"}}})
	s.reg.LoadSubsystems([]*metadata.Subsystem{{Name: name, Title: "Школа", Contents: metadata.SubsystemContents{
		Catalogs: []string{"Years", "Classes", "Private"}, Documents: []string{"Orders"}, Registers: []string{"Attendance"},
		InfoRegs: []string{"Bells"}, Processors: []string{"ClassJournal", "External"}, Journals: []string{"Documents"}, Pages: []string{"TeacherPage"},
	}, Menu: &metadata.Menu{Sections: []metadata.MenuSection{
		{ID: "education", Title: "Обучение", Titles: map[string]string{"en": "Education"}, Icon: "calendar-days", Groups: []metadata.MenuGroup{
			{ID: "school-work", Title: "Работа школы", Titles: map[string]string{"en": "School work"}, Items: []metadata.MenuItem{
				{ID: "classes", Target: "catalog:Classes"}, {ID: "orders", Target: "document:Orders"}, {ID: "bells", Target: "inforeg:Bells"}, {ID: "class-journal", Target: "processor:ClassJournal"},
			}},
			{ID: "restricted-folder", Title: "Restricted folder", Items: []metadata.MenuItem{{ID: "private", Target: "catalog:Private"}}},
		}},
		{ID: "register", Title: "Посещаемость", Items: []metadata.MenuItem{{ID: "movements", Target: "register:Attendance:movements"}, {ID: "balances", Target: "register:Attendance:balances"}}},
		{ID: "restricted-section", Title: "Restricted section", Items: []metadata.MenuItem{{ID: "private-again", Target: "catalog:Private"}}},
	}}}})
	return s
}

func semanticTeacher() *auth.User {
	return &auth.User{Login: "teacher", Roles: []*auth.Role{{Name: "Teacher", Permissions: auth.Permission{
		Catalogs: map[string][]string{"Classes": {"read"}, "Years": {"read"}}, Documents: map[string][]string{"Orders": {"read"}},
		InfoRegs: map[string][]string{"Bells": {"read"}}, Registers: map[string][]string{"Attendance": {"read"}},
		Processors: map[string][]string{"ClassJournal": {"run"}, "External": {"run"}},
	}}}}
}

func semanticAttr(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

func semanticFind(node *html.Node, attr, value string) *html.Node {
	if node.Type == html.ElementNode && semanticAttr(node, attr) == value {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := semanticFind(child, attr, value); found != nil {
			return found
		}
	}
	return nil
}

func semanticLinks(node *html.Node) []string {
	var links []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			links = append(links, semanticAttr(n, "href"))
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return links
}

func semanticHeading(node *html.Node) string {
	if node.Data == "details" {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.ElementNode && child.Data == "summary" {
				node = child
				break
			}
		}
	}
	var text strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			text.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.TrimSpace(text.String())
}

func renderSemanticNav(t *testing.T, s *Server, target, lang string, user *auth.User) *html.Node {
	t.Helper()
	s.cfg.Lang = lang
	rec := httptest.NewRecorder()
	s.index(rec, reqWithUser(target, user))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", target, rec.Code, rec.Body.String())
	}
	doc, err := html.Parse(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	nav := semanticFind(doc, "id", "ob-nav")
	if nav == nil {
		t.Fatal("missing navigation aside")
	}
	return nav
}

func TestSemanticNavigation_SchoolHierarchyLocalizationAndAccess(t *testing.T) {
	for _, collapsible := range []bool{true, false} {
		for _, lang := range []string{"ru", "en"} {
			s := semanticSchoolServer(t, "School")
			if err := s.store.SaveNavCollapsible(context.Background(), collapsible); err != nil {
				t.Fatal(err)
			}
			nav := renderSemanticNav(t, s, "/ui/?subsystem=School", lang, semanticTeacher())
			folder := semanticFind(nav, "data-nav-id", "cfg:school-work")
			if folder == nil {
				t.Fatal("missing mixed-kind folder")
			}
			wantHeading := "Работа школы"
			wantSection := "Обучение"
			if lang == "en" {
				wantHeading, wantSection = "School work", "Education"
			}
			if semanticHeading(folder) != wantHeading || semanticHeading(semanticFind(nav, "data-nav-id", "cfg:education")) != wantSection {
				t.Fatal("section/folder localization lost")
			}
			// Expanded rendering has a heading followed by its items container.
			items := folder
			if !collapsible {
				for items = folder.NextSibling; items != nil && items.Type != html.ElementNode; items = items.NextSibling {
				}
			}
			want := []string{"/ui/catalog/Classes?subsystem=School", "/ui/document/Orders?subsystem=School", "/ui/inforeg/bells?subsystem=School", "/ui/processor/classjournal?subsystem=School"}
			got := semanticLinks(items)
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Fatalf("mixed folder order: %v, want %v", got, want)
			}
			for _, id := range []string{"cfg:restricted-folder", "cfg:restricted-section", "cfg:private", "cfg:private-again"} {
				if semanticFind(nav, "data-nav-id", id) != nil {
					t.Fatalf("unreadable node survived: %s", id)
				}
			}
			classes := semanticFind(nav, "data-nav-id", "cfg:classes")
			wantTitle := "Классы"
			if lang == "en" {
				wantTitle = "Default classes"
			}
			if semanticAttr(classes, "title") != wantTitle {
				t.Fatalf("metadata fallback: %q, want %q", semanticAttr(classes, "title"), wantTitle)
			}
			links := strings.Join(semanticLinks(nav), "|")
			for _, href := range []string{"/ui/register/attendance?subsystem=School", "/ui/register/attendance/balances?subsystem=School", "/ui/journal/documents?subsystem=School", "/ui/page/TeacherPage?subsystem=School"} {
				if !strings.Contains(links, href) {
					t.Fatalf("missing %s", href)
				}
			}
			if strings.Contains(links, "/ui/processor/external") {
				t.Fatal("untrusted external visible to non-admin")
			}
			admin := renderSemanticNav(t, s, "/ui/?subsystem=School", lang, &auth.User{Login: "root", IsAdmin: true})
			if semanticFind(admin, "data-nav-id", "cfg:private") == nil || !strings.Contains(strings.Join(semanticLinks(admin), "|"), "/ui/processor/external") {
				t.Fatal("admin lost a permitted target")
			}
		}
	}
}

func TestSemanticNavigation_GlobalNilEmptyAndScoped(t *testing.T) {
	for _, mode := range []string{"nil", "empty", "scoped"} {
		s := semanticSchoolServer(t, "School")
		home := &metadata.HomePage{Menu: &metadata.Menu{Sections: []metadata.MenuSection{{ID: "home-school", Title: "School", Items: []metadata.MenuItem{{ID: "home-classes", Target: "catalog:Classes"}}}}}}
		switch mode {
		case "empty":
			home.Nav = &metadata.SubsystemContents{}
		case "scoped":
			home.Nav = &metadata.SubsystemContents{Catalogs: []string{"Classes"}, Pages: []string{"TeacherPage"}}
		}
		s.reg.LoadHomePage(home)
		nav := renderSemanticNav(t, s, "/ui/", "en", semanticTeacher())
		if semanticFind(nav, "data-nav-id", "cfg:home-school") == nil || semanticFind(nav, "data-nav-id", "cfg:home-classes") == nil {
			t.Fatalf("%s nav bypassed semantic layout", mode)
		}
		links := strings.Join(semanticLinks(nav), "|")
		if mode == "scoped" {
			if strings.Contains(links, "/ui/constants") || strings.Contains(links, "/ui/catalog/Years") || !strings.Contains(links, "/ui/page/TeacherPage") {
				t.Fatalf("scoped global membership: %s", links)
			}
		} else if !strings.Contains(links, "/ui/constants") || !strings.Contains(links, "/ui/register/attendance/balances") || strings.Contains(links, "/ui/page/TeacherPage") {
			t.Fatalf("flat global membership: %s", links)
		}
	}
}

func TestSemanticNavigation_StableIdentityEscapingAndGlobalSubsystem(t *testing.T) {
	s := semanticSchoolServer(t, "global")
	sub := s.reg.GetSubsystem("global")
	nav := renderSemanticNav(t, s, "/ui/?subsystem=global", "ru", nil)
	before := semanticAttr(semanticFind(nav, "data-nav-id", "cfg:school-work"), "id")
	sub.Menu.Sections[0].Title = `<script>alert("section")</script>`
	sub.Menu.Sections[0].Groups[0].Title = `<img src=x onerror=alert(1)>`
	sub.Menu.Sections[0].Groups[0].Items[0].Title = `<script>alert("item")</script>`
	sub.Menu.Sections[0].Groups[0].Titles = nil
	nav = renderSemanticNav(t, s, "/ui/?subsystem=global", "en", nil)
	if after := semanticAttr(semanticFind(nav, "data-nav-id", "cfg:school-work"), "id"); after != before || after == "" {
		t.Fatalf("rename/language changed identity: %q -> %q", before, after)
	}
	item := semanticFind(nav, "data-nav-id", "cfg:classes")
	if semanticAttr(item, "href") != "/ui/catalog/Classes?subsystem=global" || semanticAttr(item, "title") != `<script>alert("item")</script>` {
		t.Fatal("subsystem context or escaped title lost")
	}
	var assertNoScript func(*html.Node)
	assertNoScript = func(node *html.Node) {
		if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "img") {
			t.Fatalf("title became executable HTML: %s", node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			assertNoScript(child)
		}
	}
	assertNoScript(nav)
	s.reg.LoadHomePage(&metadata.HomePage{Menu: sub.Menu, Nav: &sub.Contents})
	home := renderSemanticNav(t, s, "/ui/", "en", nil)
	if semanticAttr(semanticFind(home, "data-nav-id", "cfg:school-work"), "id") == before {
		t.Fatal("home and subsystem named global share identity")
	}
}

func TestSemanticNavigation_DirectURLKeepsPermissions(t *testing.T) {
	s := semanticSchoolServer(t, "School")
	router := chi.NewRouter()
	router.Get("/ui/catalog/{entity}", s.list)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, reqWithUser("/ui/catalog/Private?subsystem=School", semanticTeacher()))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("direct unreadable URL: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSemanticNavigation_PrunesJournalPageAndRunOnlyTargets(t *testing.T) {
	s := semanticSchoolServer(t, "School")
	reader := &auth.User{Login: "reader", Roles: []*auth.Role{{Name: "Reader", Permissions: auth.Permission{
		Catalogs: map[string][]string{"Classes": {"read"}}, Processors: map[string][]string{"ClassJournal": {"read"}},
	}}}}
	nav := renderSemanticNav(t, s, "/ui/?subsystem=School", "en", reader)
	links := semanticLinks(nav)
	if len(links) != 1 || links[0] != "/ui/catalog/Classes?subsystem=School" {
		t.Fatalf("read without document/role/run access: %v", links)
	}
	if semanticFind(nav, "data-nav-id", "cfg:other") != nil || semanticFind(nav, "data-nav-id", "cfg:register") != nil {
		t.Fatal("empty section survived access filtering")
	}
}

func TestSemanticNavigation_InvalidMenuUsesSafeLegacyFallback(t *testing.T) {
	s := semanticSchoolServer(t, "School")
	s.reg.GetSubsystem("School").Menu.Sections[0].Groups[0].Items[0].Target = "https://example.com"
	nav := renderSemanticNav(t, s, "/ui/?subsystem=School", "en", semanticTeacher())
	if semanticFind(nav, "data-nav-id", "cfg:education") != nil || semanticFind(nav, "data-nav-id", "cfg:legacy-catalog") == nil {
		t.Fatal("invalid menu did not fall back to legacy membership")
	}
	if strings.Contains(strings.Join(semanticLinks(nav), "|"), "example.com") {
		t.Fatal("unvalidated URL exposed")
	}
}

func TestNavigationCollapseBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for collapse preference behavior tests")
	}
	cmd := exec.Command(node, "--test", "static/navigation_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("navigation browser behavior: %v\n%s", err, output)
	}
}
