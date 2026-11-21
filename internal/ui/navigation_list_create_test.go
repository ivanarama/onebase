package ui

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"golang.org/x/net/html"
)

func TestNavigationListCreateFromSidebar(t *testing.T) {
	f := newNavigationHTTPFixture(t)
	f.server.entitySvc = f.server.newEntityService(nil)
	f.server.cfg.Lang = "ru"
	ctx := context.Background()
	reader := semanticTeacher().Roles[0]
	creator := &auth.Role{Name: "Creator", Permissions: auth.Permission{
		Catalogs:  map[string][]string{"Classes": {"read", "write", "create"}},
		Documents: map[string][]string{"Orders": {"read", "write", "create"}},
	}}
	writer := &auth.Role{Name: "Writer", Permissions: auth.Permission{
		Catalogs:  map[string][]string{"Classes": {"read", "write"}},
		Documents: map[string][]string{"Orders": {"read", "write"}},
	}}
	if err := f.server.authRepo.SyncRoles(ctx, []*auth.Role{reader, creator, writer}); err != nil {
		t.Fatal(err)
	}
	for _, role := range []*auth.Role{creator, writer} {
		login := strings.ToLower(role.Name)
		user, err := f.server.authRepo.Create(ctx, login, "test-password", login, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.server.authRepo.AssignRole(ctx, user.ID, role.ID); err != nil {
			t.Fatal(err)
		}
		token, err := f.server.authRepo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
		if err != nil {
			t.Fatal(err)
		}
		f.cookie[login] = &http.Cookie{Name: "onebase_session", Value: token}
	}
	if err := f.server.store.Migrate(ctx, f.server.reg.Entities()); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		login  string
		status int
	}{{"creator", http.StatusOK}, {"writer", http.StatusForbidden}, {"alice", http.StatusForbidden}} {
		t.Run(tc.login, func(t *testing.T) {
			r := f.request(t, "GET", "/ui/?subsystem=School", tc.login, nil)
			if r.Code != http.StatusOK {
				t.Fatalf("sidebar: HTTP %d", r.Code)
			}
			doc, err := html.Parse(strings.NewReader(r.Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			for _, menu := range []struct{ id, path string }{
				{"classes", "/ui/catalog/Classes"}, {"orders", "/ui/document/Orders"},
			} {
				list := semanticFind(doc, "data-nav-id", "cfg:"+menu.id)
				if list == nil || semanticAttr(list, "href") != menu.path+"?subsystem=School" {
					t.Fatalf("%s list link missing or changed", menu.id)
				}
				plus := semanticFind(list.Parent, "data-nav-list-create", "1")
				if plus == nil || !strings.HasPrefix(semanticAttr(plus, "aria-label"), "Создать: ") {
					t.Fatalf("%s: missing accessible create action for %s", menu.id, tc.login)
				}
				link, err := url.Parse(semanticAttr(plus, "href"))
				if err != nil || link.Path != menu.path+"/new" || link.Query().Get("subsystem") != "School" {
					t.Fatalf("bad form URL %v: %v", link, err)
				}
				form := f.request(t, "GET", link.String(), tc.login, nil)
				if form.Code != tc.status || (tc.status == http.StatusForbidden && !strings.Contains(form.Body.String(), "Недостаточно прав")) {
					t.Fatalf("%s via create action: HTTP %d, want %d", menu.id, form.Code, tc.status)
				}
			}
			if tc.login == "alice" {
				for _, id := range []string{"class-journal", "bells", "movements", "balances"} {
					link := semanticFind(doc, "data-nav-id", "cfg:"+id)
					if link == nil || semanticAttr(link.Parent, "class") == "nav-list-row" {
						t.Fatalf("%s: secondary create action on a non-object link", id)
					}
				}
			}
		})
	}
}
