package ui

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/navigation"
	"golang.org/x/net/html"
)

// nav_by_role в home_page подсистемы — меню раздела по ролям, как журналы
// интерфейса роли 1С: кладовщик читает заказы ради ссылок из ордера, но в его
// меню их нет. Состав роли только сужает contents подсистемы.
func subsystemRoleNav(t *testing.T, f navigationHTTPFixture, login string) *html.Node {
	t.Helper()
	response := f.request(t, "GET", "/ui/?subsystem=School", login, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("subsystem home: %d %s", response.Code, response.Body.String())
	}
	doc, err := html.Parse(strings.NewReader(response.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	nav := semanticFind(doc, "id", "ob-nav")
	if nav == nil {
		t.Fatal("subsystem navigation missing")
	}
	return nav
}

func TestSubsystemNavByRole_HTTPMenuAndPersonalScope(t *testing.T) {
	f := newNavigationHTTPFixture(t)
	f.server.reg.GetSubsystem("School").HomePage = &metadata.HomePage{NavByRole: map[string]*metadata.SubsystemContents{
		// Years — объект не из contents подсистемы: состав роли его не добавляет.
		"Teacher": {Catalogs: []string{"Classes", "NotInSubsystem"}, Documents: []string{"Orders"}},
	}}

	links := strings.Join(semanticLinks(subsystemRoleNav(t, f, "alice")), "|")
	for _, want := range []string{"/ui/catalog/Classes?subsystem=School", "/ui/document/Orders?subsystem=School"} {
		if !strings.Contains(links, want) {
			t.Fatalf("роль Teacher потеряла %s: %s", want, links)
		}
	}
	for _, hidden := range []string{"/ui/inforeg/bells", "/ui/processor/classjournal", "/ui/register/attendance"} {
		if strings.Contains(links, hidden) {
			t.Fatalf("%s читается ролью, но в её меню раздела его быть не должно: %s", hidden, links)
		}
	}
	if admin := strings.Join(semanticLinks(subsystemRoleNav(t, f, "admin")), "|"); !strings.Contains(admin, "/ui/inforeg/bells") {
		t.Fatalf("администратор видит общее меню раздела: %s", admin)
	}

	// Личный редактор раздела — в том же составе; объект вне меню роли не
	// сохранить и подделанной формой.
	state := f.personalEditor(t, "alice", "School")
	for _, tree := range []string{treeTargets(state.Base), treeTargets(state.Desired)} {
		if strings.Contains(tree, "inforeg:Bells") || !strings.Contains(tree, "catalog:Classes") {
			t.Fatalf("личный редактор раздела вне состава роли: %s", tree)
		}
	}
	forged := state
	forged.Desired = f.editor(t, "School").Desired
	for _, action := range []string{"preview", "save"} {
		rec := f.request(t, "POST", "/ui/settings/navigation/"+action, "alice", personalNavigationForm(t, forged))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("сохранение вне меню роли (%s): %d %s", action, rec.Code, rec.Body.String())
		}
	}
}

func treeTargets(tree navigation.Tree) string {
	var out []string
	for _, sec := range tree.Sections {
		for _, it := range sec.Items {
			out = append(out, it.Target)
		}
		for _, g := range sec.Groups {
			for _, it := range g.Items {
				out = append(out, it.Target)
			}
		}
	}
	return strings.Join(out, ",")
}

func TestNavByRole_HTTPCaseInsensitiveObjectNames(t *testing.T) {
	for _, context := range []string{"home-placed", "home-role-only", "subsystem"} {
		t.Run(context, func(t *testing.T) {
			f := newNavigationHTTPFixture(t)
			role := &metadata.SubsystemContents{
				Catalogs: []string{"classes", "CLASSES", "years"}, Documents: []string{"orders"},
				Registers: []string{"attendance"}, InfoRegs: []string{"bells"},
				Processors: []string{"classjournal"}, Journals: []string{"documents"}, Pages: []string{"teacherpage"},
			}
			home := &metadata.HomePage{NavByRole: map[string]*metadata.SubsystemContents{"Teacher": role}}
			sub := ""
			var nav *html.Node
			if context == "subsystem" {
				sub = "School"
				current := f.server.reg.GetSubsystem(sub)
				current.Contents.Catalogs = []string{"Classes", "Private"}
				current.HomePage = home
				nav = subsystemRoleNav(t, f, "alice")
			} else {
				if context == "home-placed" {
					common := f.server.reg.GetSubsystem("School").Contents
					home.Nav = &common
				} else {
					home.Nav = &metadata.SubsystemContents{Catalogs: []string{"Private"}}
				}
				f.server.reg.LoadHomePage(home)
				nav = roleHomeNav(t, f, "alice")
			}
			links := strings.Join(semanticLinks(nav), "|")
			if !strings.Contains(links, "/ui/catalog/Classes") || strings.Contains(links, "/ui/catalog/classes") {
				t.Fatalf("canonical Classes link missing: %s", links)
			}
			if sub != "" && strings.Contains(links, "/ui/catalog/Years") {
				t.Fatalf("role expanded subsystem contents: %s", links)
			}
			state := f.personalEditor(t, "alice", sub)
			want := []string{"catalog:Classes", "document:Orders", "register:Attendance:movements", "register:Attendance:balances",
				"inforeg:Bells", "processor:ClassJournal", "journal:Documents", "page:TeacherPage"}
			if sub == "" {
				want = append(want, "catalog:Years")
			}
			for _, tree := range []navigation.Tree{state.Base, state.Desired} {
				targets := strings.Split(treeTargets(tree), ",")
				if len(targets) != len(want) {
					t.Fatalf("role palette has missing or duplicate targets: %v, want %v", targets, want)
				}
				for _, target := range want {
					if !strings.Contains(","+treeTargets(tree)+",", ","+target+",") {
						t.Fatalf("canonical role target %s missing: %v", target, targets)
					}
				}
			}
			preview := f.request(t, "POST", "/ui/settings/navigation/preview", "alice", personalNavigationForm(t, state))
			if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), "/ui/catalog/Classes") {
				t.Fatalf("canonical preview: %d %s", preview.Code, preview.Body.String())
			}
			personalSave(t, f, "alice", state)
			// Runtime resolution must not rewrite the loaded role metadata.
			if role.Catalogs[0] != "classes" || role.Documents[0] != "orders" {
				t.Fatal("role metadata mutated")
			}
		})
	}
}
