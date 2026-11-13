package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// /ui/app must do the same database work and render the same HTML with a
// role-hidden widget as without it. Its reference controls must never load.
func TestAppShellHiddenWidgetDoesNotLoadPresentation(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ref := &metadata.Entity{Name: "FilterChoices", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}}}
		denied := &metadata.Entity{Name: "HiddenSource", Kind: metadata.KindCatalog, Fields: ref.Fields}
		s := newSubmitTestServerOn(t, db, []*metadata.Entity{ref, denied})
		s.reg.LoadWidgets([]*metadata.Widget{
			{Name: "Visible", Type: metadata.WidgetTypeActions, Items: []metadata.WidgetAction{{Label: "Home", URL: "/ui/"}}},
			{Name: "Hidden", Type: metadata.WidgetTypeList, Query: "ВЫБРАТЬ Name ИЗ Справочник.HiddenSource", Filters: []metadata.WidgetFilter{{Name: "Choice", Type: "reference:FilterChoices", Param: "Choice"}}},
		})
		router := chi.NewRouter()
		s.Mount(router)
		for _, roles := range [][]string{{"Operator", "Messenger"}, {"Other"}, {"OTK", "Operator"}} {
			t.Run(strings.Join(roles, "+"), func(t *testing.T) {
				user := &auth.User{ID: "synthetic-user", Login: "synthetic-user"}
				for _, role := range roles {
					user.Roles = append(user.Roles, &auth.Role{Name: role, Permissions: auth.Permission{Catalogs: map[string][]string{ref.Name: {"read"}}}})
				}
				// The widget source is denied; reference choices remain readable.
				get := func(hidden bool) (string, int64) {
					names := []string{"Visible"}
					if hidden {
						names = append(names, "Hidden")
					}
					s.reg.LoadHomePage(&metadata.HomePage{Layout: "rows", Rows: []metadata.HomePageRow{{Widgets: names}}})
					req := httptest.NewRequest(http.MethodGet, "/ui/app?home=1", nil)
					req = req.WithContext(auth.ContextWithUser(req.Context(), user))
					rec := httptest.NewRecorder()
					before := int64(0)
					if stats := db.PoolStats(); stats != nil {
						before = stats.AcquireCount
					}
					router.ServeHTTP(rec, req)
					if rec.Code != 200 {
						t.Fatalf("GET %d %.300s", rec.Code, rec.Body.String())
					}
					count := int64(0)
					if stats := db.PoolStats(); stats != nil {
						count = stats.AcquireCount - before
					}
					return rec.Body.String(), count
				}
				get(false) // warm common service tables and template paths
				want, baseQueries := get(false)
				got, hiddenQueries := get(true)
				if got != want {
					t.Fatal("a hidden widget changed rendered HTML")
				}
				if hiddenQueries != baseQueries {
					t.Fatalf("hidden widget performs presentation SQL: baseline=%d hidden=%d", baseQueries, hiddenQueries)
				}
				t.Logf("same HTML; database acquisitions baseline=%d hidden=%d", baseQueries, hiddenQueries)
			})
		}
	})
}
