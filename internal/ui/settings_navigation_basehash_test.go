package ui

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/navigation"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

func TestPersonalNavigation_CommonBaseHashHTTP(t *testing.T) {
	for _, roleOnly := range []bool{false, true} {
		name := "common-target"
		if roleOnly {
			name = "role-only-target"
		}
		t.Run(name, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				f, switchRole := roleIntentFixture(t, db, roleOnly)
				common := f.editor(t, "")
				years := roleIntentItem(t, &common.Desired, "catalog:Years")
				years.Title, years.Titles = "Common years", nil
				if got := f.request(t, "POST", "/ui/admin/navigation/save", "admin", navigationForm(t, common)); got.Code != http.StatusSeeOther {
					t.Fatalf("save common: %d %s", got.Code, got.Body.String())
				}
				scope := storage.NavigationSettingsScope{Layer: navigation.AdminLayer, Context: "global"}
				before, err := db.GetNavigationSettings(context.Background(), scope)
				if err != nil || !before.Exists {
					t.Fatalf("stored common: %+v %v", before, err)
				}
				check := func(login string, warning bool) navigationBootstrap {
					t.Helper()
					response := f.request(t, "GET", "/ui/settings/navigation", login, nil)
					if response.Code != http.StatusOK {
						t.Fatalf("personal %s: %d", login, response.Code)
					}
					doc, err := html.Parse(strings.NewReader(response.Body.String()))
					if err != nil {
						t.Fatal(err)
					}
					page := semanticFind(doc, "id", "navigation-settings")
					if page == nil {
						t.Fatal("missing navigation settings page")
					}
					if got := semanticFind(page, "class", "error") != nil; got != warning {
						t.Fatalf("%s common-base warning=%v, want %v", login, got, warning)
					}
					return decodeNavigationBootstrap(t, response)
				}
				for _, login := range []string{"admin", "alice", "bob"} {
					state := check(login, false)
					if roleIntentItem(t, &state.Desired, "catalog:Years").Title != "Common years" {
						t.Fatalf("%s lost common delta", login)
					}
				}
				personal := f.personalEditor(t, "alice", "")
				years = roleIntentItem(t, &personal.Desired, "catalog:Years")
				years.Title, years.Titles = "Personal years", nil
				personal.Renamed = []string{years.ID}
				personalSave(t, f, "alice", personal)
				if state := check("alice", false); roleIntentItem(t, &state.Desired, "catalog:Years").Title != "Personal years" {
					t.Fatal("personal delta lost")
				}
				switchRole(true)
				check("alice", false)
				restricted := f.request(t, "GET", "/ui/settings/navigation", "alice", nil)
				if strings.Contains(restricted.Body.String(), "catalog:Classes") {
					t.Fatal("role-only target leaked to restricted editor")
				}
				switchRole(false)
				check("alice", false)
				after, err := db.GetNavigationSettings(context.Background(), scope)
				if err != nil || after.Raw != before.Raw || after.Revision != before.Revision {
					t.Fatal("reading/role changes rewrote stored common delta")
				}
				// A real metadata change must still warn with the same stored delta.
				entity := f.server.reg.GetEntity("Years")
				entity.Title, entity.Titles = "Changed configuration title", nil
				for _, login := range []string{"admin", "alice", "bob"} {
					check(login, true)
				}
			})
		})
	}
}
