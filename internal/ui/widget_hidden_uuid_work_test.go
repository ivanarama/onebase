package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// The public shell must not look up an arbitrary UUID from a column which is
// absent from the rendered list. The visible-column path still checks access.
func TestAppShellHiddenUUIDDoesNotLoadPresentation(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		source := &metadata.Entity{Name: "WidgetProbe", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}, {Name: "HiddenUUID", Type: metadata.FieldTypeString}}}
		ref := &metadata.Entity{Name: "WidgetReference", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}}}
		s := newSubmitTestServerOn(t, db, []*metadata.Entity{source, ref})
		s.widgetCache = nil // force each public GET to exercise its real runner
		refID := uuid.New()
		if err := db.Upsert(t.Context(), ref.Name, refID, map[string]any{"Name": "reference-safe-label"}, ref); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(t.Context(), source.Name, uuid.New(), map[string]any{"Name": "shown-value", "HiddenUUID": refID.String()}, source); err != nil {
			t.Fatal(err)
		}
		w := &metadata.Widget{Name: "Probe", Type: metadata.WidgetTypeList, Columns: []metadata.WidgetColumn{{Field: "Name"}}}
		s.reg.LoadWidgets([]*metadata.Widget{w})
		s.reg.LoadHomePage(&metadata.HomePage{Layout: "rows", Rows: []metadata.HomePageRow{{Widgets: []string{w.Name}}}})
		router := chi.NewRouter()
		s.Mount(router)
		for _, allowRef := range []bool{true, false} {
			t.Run(map[bool]string{true: "readable-reference", false: "denied-reference"}[allowRef], func(t *testing.T) {
				permissions := map[string][]string{source.Name: {"read"}}
				if allowRef {
					permissions[ref.Name] = []string{"read"}
				}
				user := &auth.User{ID: "own-test-user", Login: "own-test-user", Roles: []*auth.Role{{Name: "Operator", Permissions: auth.Permission{Catalogs: permissions}}}}
				get := func() (string, int64) {
					req := httptest.NewRequest(http.MethodGet, "/ui/app?home=1", nil)
					req = req.WithContext(auth.ContextWithUser(req.Context(), user))
					before := int64(0)
					if stats := db.PoolStats(); stats != nil {
						before = stats.AcquireCount
					}
					rec := httptest.NewRecorder()
					router.ServeHTTP(rec, req)
					if rec.Code != 200 {
						t.Fatalf("GET status%d", rec.Code)
					}
					count := int64(0)
					if stats := db.PoolStats(); stats != nil {
						count = stats.AcquireCount - before
					}
					return rec.Body.String(), count
				}
				w.Columns = []metadata.WidgetColumn{{Field: "Name"}}
				w.Query = "ВЫБРАТЬ Name ИЗ Справочник.WidgetProbe"
				get()
				want, baseQueries := get()
				w.Query = "ВЫБРАТЬ Name, HiddenUUID ИЗ Справочник.WidgetProbe"
				got, hiddenQueries := get()
				if got != want {
					t.Fatal("hidden UUID changed visible HTML")
				}
				if hiddenQueries != baseQueries {
					t.Fatalf("hidden UUID performs lookup SQL: baseline%d hidden%d", baseQueries, hiddenQueries)
				}
				w.Columns = append(w.Columns, metadata.WidgetColumn{Field: "HiddenUUID"})
				visible, _ := get()
				if allowRef && !strings.Contains(visible, "reference-safe-label") {
					t.Fatal("visible UUID lost its label")
				}
				if !allowRef && strings.Contains(visible, "reference-safe-label") {
					t.Fatal("label leaked from denied reference")
				}
				t.Logf("same HTML and acquisitions without/with hidden column: %d/%d; visible label access preserved", baseQueries, hiddenQueries)
			})
		}
	})
}
